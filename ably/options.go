package ably

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ably/ably-go/ably/internal/ablyutil"
	"github.com/ably/ably-go/ably/proto"
)

const (
	protocolJSON    = "application/json"
	protocolMsgPack = "application/x-msgpack"

	// RestHost is the primary ably host .
	RestHost = "rest.ably.io"
)

var defaultOptions = &ClientOptions{
	RestHost:                 RestHost,
	FallbackHosts:            DefaultFallbackHosts(),
	HTTPMaxRetryCount:        3,
	RealtimeHost:             "realtime.ably.io",
	TimeoutDisconnect:        30 * time.Second,
	RealtimeRequestTimeout:   10 * time.Second, // DF1b
	DisconnectedRetryTimeout: 15 * time.Second, // TO3l1
	TimeoutSuspended:         2 * time.Minute,
	FallbackRetryTimeout:     10 * time.Minute,
	IdempotentRestPublishing: false,
}

func DefaultFallbackHosts() []string {
	return []string{
		"a.ably-realtime.com",
		"b.ably-realtime.com",
		"c.ably-realtime.com",
		"d.ably-realtime.com",
		"e.ably-realtime.com",
	}
}

const (
	authBasic = 1 + iota
	authToken
)

type AuthOptions struct {
	// AuthCallback is called in order to obtain a signed token request.
	//
	// This enables a client to obtain token requests from another entity,
	// so tokens can be renewed without the client requiring access to keys.
	AuthCallback func(context.Context, TokenParams) (Tokener, error)

	// URL which is queried to obtain a signed token request.
	//
	// This enables a client to obtain token requests from another entity,
	// so tokens can be renewed without the client requiring access to keys.
	//
	// If AuthURL is non-empty and AuthCallback is nil, the Ably library
	// builds a req (*http.Request) which then is issued against the given AuthURL
	// in order to obtain authentication token. The response is expected to
	// carry a single token string in the payload when Content-Type header
	// is "text/plain" or JSON-encoded *ably.TokenDetails when the header
	// is "application/json".
	//
	// The req is built with the following values:
	//
	// GET requests:
	//
	//   - req.URL.RawQuery is encoded from *TokenParams and AuthParams
	//   - req.Header is set to AuthHeaders
	//
	// POST requests:
	//
	//   - req.Header is set to AuthHeaders
	//   - Content-Type is set to "application/x-www-form-urlencoded" and
	//     the payload is encoded from *TokenParams and AuthParams
	//
	AuthURL string

	// Key obtained from the dashboard.
	Key string

	// Token is an authentication token issued for this application against
	// a specific key and TokenParams.
	Token string

	// TokenDetails is an authentication token issued for this application against
	// a specific key and TokenParams.
	TokenDetails *TokenDetails

	// AuthMethod specifies which method, GET or POST, is used to query AuthURL
	// for the token information (*ably.TokenRequest or *ablyTokenDetails).
	//
	// If empty, GET is used by default.
	AuthMethod string

	// AuthHeaders are HTTP request headers to be included in any request made
	// to the AuthURL.
	AuthHeaders http.Header

	// AuthParams are HTTP query parameters to be included in any requset made
	// to the AuthURL.
	AuthParams url.Values

	// UseQueryTime when set to true, the time queried from Ably servers will
	// be used to sign the TokenRequest instead of using local time.
	UseQueryTime bool

	// Spec: TO3j11
	DefaultTokenParams *TokenParams

	// UseTokenAuth makes the Rest and Realtime clients always use token
	// authentication method.
	UseTokenAuth bool

	// Force when true makes the client request new token unconditionally.
	//
	// By default the client does not request new token if the current one
	// is still valid.
	Force bool
}

func (opts *AuthOptions) externalTokenAuthSupported() bool {
	return !(opts.Token == "" && opts.TokenDetails == nil && opts.AuthCallback == nil && opts.AuthURL == "")
}

func (opts *AuthOptions) merge(extra *AuthOptions, defaults bool) *AuthOptions {
	ablyutil.Merge(opts, extra, defaults)
	return opts
}

func (opts *AuthOptions) authMethod() string {
	if opts.AuthMethod != "" {
		return opts.AuthMethod
	}
	return "GET"
}

// KeyName gives the key name parsed from the Key field.
func (opts *AuthOptions) KeyName() string {
	if i := strings.IndexRune(opts.Key, ':'); i != -1 {
		return opts.Key[:i]
	}
	return ""
}

// KeySecret gives the key secret parsed from the Key field.
func (opts *AuthOptions) KeySecret() string {
	if i := strings.IndexRune(opts.Key, ':'); i != -1 {
		return opts.Key[i+1:]
	}
	return ""
}

type ClientOptions struct {
	AuthOptions

	RestHost                string // optional; overwrite endpoint hostname for REST client
	FallbackHostsUseDefault bool

	FallbackHosts   []string
	RealtimeHost    string        // optional; overwrite endpoint hostname for Realtime client
	Environment     string        // optional; prefixes both hostname with the environment string
	Port            int           // optional: port to use for non-TLS connections and requests
	TLSPort         int           // optional: port to use for TLS connections and requests
	ClientID        string        // optional; required for managing realtime presence of the current client
	Recover         string        // optional; used to recover client state
	Logger          LoggerOptions // optional; overwrite logging defaults
	TransportParams map[string]string

	// max number of fallback hosts to use as a fallback.
	HTTPMaxRetryCount int

	// The period in milliseconds before HTTP requests are retried against the
	// default endpoint
	//
	// spec TO3l10
	FallbackRetryTimeout time.Duration

	NoTLS            bool // when true REST and realtime client won't use TLS
	NoConnect        bool // when true realtime client will not attempt to connect automatically
	NoEcho           bool // when true published messages will not be echoed back
	NoQueueing       bool // when true drops messages published during regaining connection
	NoBinaryProtocol bool // when true uses JSON for network serialization protocol instead of MsgPack

	// When true idempotent rest publishing will be enabled.
	// Spec TO3n
	IdempotentRestPublishing bool

	// TimeoutConnect is the time period after which connect request is failed.
	//
	// Deprecated: use RealtimeRequestTimeout instead.
	TimeoutConnect    time.Duration
	TimeoutDisconnect time.Duration // time period after which disconnect request is failed
	TimeoutSuspended  time.Duration // time period after which no more reconnection attempts are performed

	// RealtimeRequestTimeout is the timeout for realtime connection establishment
	// and each subsequent operation.
	RealtimeRequestTimeout time.Duration

	// DisconnectedRetryTimeout is the time to wait after a disconnection before
	// attempting an automatic reconnection, if still disconnected.
	DisconnectedRetryTimeout time.Duration

	// Dial specifies the dial function for creating message connections used
	// by Realtime.
	//
	// If Dial is nil, the default websocket connection is used.
	Dial func(protocol string, u *url.URL) (proto.Conn, error)

	// Listener if set, will be automatically registered with On method for every
	// realtime connection and realtime channel created by realtime client.
	// The listener will receive events for all state transitions.
	Listener chan<- State

	// HTTPClient specifies the client used for HTTP communication by RestClient.
	//
	// If HTTPClient is nil, the http.DefaultClient is used.
	HTTPClient *http.Client

	//When provided this will be used on every request.
	Trace *httptrace.ClientTrace
}

func NewClientOptions(key string) *ClientOptions {
	return &ClientOptions{
		AuthOptions: AuthOptions{
			Key: key,
		},
	}
}

func (opts *ClientOptions) timeoutConnect() time.Duration {
	if opts.TimeoutConnect != 0 {
		return opts.TimeoutConnect
	}
	return defaultOptions.RealtimeRequestTimeout
}

func (opts *ClientOptions) timeoutDisconnect() time.Duration {
	if opts.TimeoutDisconnect != 0 {
		return opts.TimeoutDisconnect
	}
	return defaultOptions.TimeoutDisconnect
}

func (opts *ClientOptions) timeoutSuspended() time.Duration {
	if opts.TimeoutSuspended != 0 {
		return opts.TimeoutSuspended
	}
	return defaultOptions.TimeoutSuspended
}

func (opts *ClientOptions) fallbackRetryTimeout() time.Duration {
	if opts.FallbackRetryTimeout != 0 {
		return opts.FallbackRetryTimeout
	}
	return defaultOptions.FallbackRetryTimeout
}

func (opts *ClientOptions) realtimeRequestTimeout() time.Duration {
	if opts.RealtimeRequestTimeout != 0 {
		return opts.RealtimeRequestTimeout
	}
	return defaultOptions.RealtimeRequestTimeout
}

func (opts *ClientOptions) disconnectedRetryTimeout() time.Duration {
	if opts.DisconnectedRetryTimeout != 0 {
		return opts.DisconnectedRetryTimeout
	}
	return defaultOptions.DisconnectedRetryTimeout
}

func (opts *ClientOptions) restURL() string {
	host := opts.RestHost
	if host == "" {
		host = defaultOptions.RestHost
		if opts.Environment == "production" {
			opts.Environment = ""
		}
		if opts.Environment != "" {
			host = opts.Environment + "-" + host
		}
	}
	if opts.NoTLS {
		port := opts.Port
		if port == 0 {
			port = 80
		}
		return "http://" + net.JoinHostPort(host, strconv.FormatInt(int64(port), 10))
	} else {
		port := opts.TLSPort
		if port == 0 {
			port = 443
		}
		return "https://" + net.JoinHostPort(host, strconv.FormatInt(int64(port), 10))
	}
}

func (opts *ClientOptions) realtimeURL() string {
	host := opts.RealtimeHost
	if host == "" {
		host = defaultOptions.RealtimeHost
		if opts.Environment == "production" {
			opts.Environment = ""
		}
		if opts.Environment != "" {
			host = opts.Environment + "-" + host
		}
	}
	if opts.NoTLS {
		port := opts.Port
		if port == 0 {
			port = 80
		}
		return "ws://" + net.JoinHostPort(host, strconv.FormatInt(int64(port), 10))
	} else {
		port := opts.TLSPort
		if port == 0 {
			port = 443
		}
		return "wss://" + net.JoinHostPort(host, strconv.FormatInt(int64(port), 10))
	}
}

func (opts *ClientOptions) httpclient() *http.Client {
	if opts.HTTPClient != nil {
		return opts.HTTPClient
	}
	return http.DefaultClient
}

func (opts *ClientOptions) protocol() string {
	if opts.NoBinaryProtocol {
		return protocolJSON
	}
	return protocolMsgPack
}

func (opts *ClientOptions) idempotentRestPublishing() bool {
	return opts.IdempotentRestPublishing
}

// Time returns the given time as a timestamp in milliseconds since epoch.
func Time(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

// TimeNow returns current time as a timestamp in milliseconds since epoch.
func TimeNow() int64 {
	return Time(time.Now())
}

// Duration returns converts the given duration to milliseconds.
func Duration(d time.Duration) int64 {
	return int64(d / time.Millisecond)
}

// This needs to use a timestamp in millisecond
// Use the previous function to generate them from a time.Time struct.
type ScopeParams struct {
	Start int64
	End   int64
	Unit  string
}

func (s *ScopeParams) EncodeValues(out *url.Values) error {
	if s.Start != 0 && s.End != 0 && s.Start > s.End {
		return fmt.Errorf("start must be before end")
	}
	if s.Start != 0 {
		out.Set("start", strconv.FormatInt(s.Start, 10))
	}
	if s.End != 0 {
		out.Set("end", strconv.FormatInt(s.End, 10))
	}
	if s.Unit != "" {
		out.Set("unit", s.Unit)
	}
	return nil
}

type PaginateParams struct {
	ScopeParams
	Limit     int
	Direction string
}

func (p *PaginateParams) EncodeValues(out *url.Values) error {
	if p.Limit < 0 {
		out.Set("limit", strconv.Itoa(100))
	} else if p.Limit != 0 {
		out.Set("limit", strconv.Itoa(p.Limit))
	}
	switch p.Direction {
	case "":
		break
	case "backwards", "forwards":
		out.Set("direction", p.Direction)
		break
	default:
		return fmt.Errorf("Invalid value for direction: %s", p.Direction)
	}
	p.ScopeParams.EncodeValues(out)
	return nil
}

type ClientOptionsV12 []func(*ClientOptions)

// A Tokener is or can be used to get a TokenDetails.
type Tokener interface {
	IsTokener()
	isTokener()
}

// A TokenString is the string representation of an authentication token.
type TokenString string

func (TokenString) IsTokener() {}
func (TokenString) isTokener() {}

func (os ClientOptionsV12) AuthCallback(authCallback func(context.Context, TokenParams) (Tokener, error)) ClientOptionsV12 {
	return append(os, func(os *ClientOptions) {
		os.AuthCallback = authCallback
	})
}

func (os ClientOptionsV12) DefaultTokenParams(params TokenParams) ClientOptionsV12 {
	return append(os, func(os *ClientOptions) {
		os.DefaultTokenParams = &params
	})
}

func (os ClientOptionsV12) applyWithDefaults(to *ClientOptions) {
	for _, set := range os {
		set(to)
	}

	if to.DefaultTokenParams == nil {
		to.DefaultTokenParams = &TokenParams{
			TTL: int64(60 * time.Minute / time.Millisecond),
		}
	}
}
