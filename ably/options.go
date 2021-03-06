package ably

import (
	"errors"
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
	RestHost     = "rest.ably.io"
	RealtimeHost = "realtime.ably.io"
	Port         = 80
	TLSPort      = 443
)

var defaultOptions = &ClientOptions{
	RestHost:                 RestHost,
	FallbackHosts:            defaultFallbackHosts(),
	HTTPMaxRetryCount:        3,
	HTTPRequestTimeout:       10 * time.Second,
	RealtimeHost:             RealtimeHost,
	TimeoutDisconnect:        30 * time.Second,
	RealtimeRequestTimeout:   10 * time.Second, // DF1b
	DisconnectedRetryTimeout: 15 * time.Second, // TO3l1
	TimeoutSuspended:         2 * time.Minute,
	FallbackRetryTimeout:     10 * time.Minute,
	IdempotentRestPublishing: false,
	Port:                     Port,
	TLSPort:                  TLSPort,
}

func defaultFallbackHosts() []string {
	return []string{
		"a.ably-realtime.com",
		"b.ably-realtime.com",
		"c.ably-realtime.com",
		"d.ably-realtime.com",
		"e.ably-realtime.com",
	}
}

func getEnvFallbackHosts(env string) []string {
	return []string{
		fmt.Sprintf("%s-%s", env, "a-fallback.ably-realtime.com"),
		fmt.Sprintf("%s-%s", env, "b-fallback.ably-realtime.com"),
		fmt.Sprintf("%s-%s", env, "c-fallback.ably-realtime.com"),
		fmt.Sprintf("%s-%s", env, "d-fallback.ably-realtime.com"),
		fmt.Sprintf("%s-%s", env, "e-fallback.ably-realtime.com"),
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
	//
	// The returned value of the token is expected to be one of the following
	// types:
	//
	//   - string, which is then used as token string
	//   - *ably.TokenRequest, which is then used as an already signed request
	//   - *ably.TokenDetails, which is then used as a token
	//
	AuthCallback func(params *TokenParams) (token interface{}, err error)

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

	RestHost string // optional; overwrite endpoint hostname for REST client

	// Deprecated: The library will automatically use default fallback hosts when a custom REST host or custom fallback hosts aren't provided.
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
	// HTTPRequestTimeout is the timeout for getting a response for outgoing HTTP requests.
	//
	// Will only be used if no custom HTTPClient is set.
	HTTPRequestTimeout time.Duration

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
	// by RealtimeClient.
	//
	// If Dial is nil, the default websocket connection is used.
	Dial func(protocol string, u *url.URL) (proto.Conn, error)

	// Listener if set, will be automatically registered with On method for every
	// realtime connection and realtime channel created by realtime client.
	// The listener will receive events for all state transitions.
	Listener chan<- State

	// HTTPClient specifies the client used for HTTP communication by RestClient.
	//
	// If HTTPClient is nil, a client configured with default settings is used.
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

func (opts *ClientOptions) validate() error {
	_, err := opts.getFallbackHosts()
	if err != nil {
		log := opts.Logger.Sugar()
		log.Errorf("Error getting fallbackHosts : %v", err.Error())
		return err
	}
	return nil
}

func (opts *ClientOptions) isProductionEnvironment() bool {
	env := opts.Environment
	return empty(env) || strings.EqualFold(env, "production")
}

func (opts *ClientOptions) activePort() (port int, isDefault bool) {
	if opts.NoTLS {
		port = opts.Port
		if port == 0 {
			port = defaultOptions.Port
		}
		if port == defaultOptions.Port {
			isDefault = true
		}
		return
	}
	port = opts.TLSPort
	if port == 0 {
		port = defaultOptions.TLSPort
	}
	if port == defaultOptions.TLSPort {
		isDefault = true
	}
	return
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

func (opts *ClientOptions) getRestHost() string {
	if !empty(opts.RestHost) {
		return opts.RestHost
	}
	if !opts.isProductionEnvironment() {
		return opts.Environment + "-" + defaultOptions.RestHost
	}
	return defaultOptions.RestHost
}

func (opts *ClientOptions) getRealtimeHost() string {
	if !empty(opts.RealtimeHost) {
		return opts.RealtimeHost
	}
	if !empty(opts.RestHost) {
		logger := opts.Logger.Sugar()
		logger.Warnf("restHost is set to %s but realtimeHost is not set so setting realtimeHost to %s too. If this is not what you want, please set realtimeHost explicitly.", opts.RestHost, opts.RealtimeHost)
		return opts.RestHost
	}
	if !opts.isProductionEnvironment() {
		return opts.Environment + "-" + defaultOptions.RealtimeHost
	}
	return defaultOptions.RealtimeHost
}

func empty(s string) bool {
	return len(strings.TrimSpace(s)) == 0
}

func (opts *ClientOptions) restURL() (restUrl string) {
	restHost := opts.getRestHost()
	port, _ := opts.activePort()
	baseUrl := net.JoinHostPort(restHost, strconv.Itoa(port))
	if opts.NoTLS {
		return "http://" + baseUrl
	}
	return "https://" + baseUrl
}

func (opts *ClientOptions) realtimeURL() (realtimeUrl string) {
	realtimeHost := opts.getRealtimeHost()
	port, _ := opts.activePort()
	baseUrl := net.JoinHostPort(realtimeHost, strconv.Itoa(port))
	if opts.NoTLS {
		return "ws://" + baseUrl
	}
	return "wss://" + baseUrl
}

func (opts *ClientOptions) getFallbackHosts() ([]string, error) {
	logger := opts.Logger.Sugar()
	_, isDefaultPort := opts.activePort()
	if opts.FallbackHostsUseDefault {
		if opts.FallbackHosts != nil {
			return nil, errors.New("fallbackHosts and fallbackHostsUseDefault cannot both be set")
		}
		if !isDefaultPort {
			return nil, errors.New("fallbackHostsUseDefault cannot be set when port or tlsPort are set")
		}
		if !empty(opts.Environment) {
			logger.Warn("Deprecated fallbackHostsUseDefault : There is no longer a need to set this when the environment option is also set since the library can generate the correct fallback hosts using the environment option.")
		}
		logger.Warn("Deprecated fallbackHostsUseDefault : using default fallbackhosts")
		return defaultOptions.FallbackHosts, nil
	}
	if opts.FallbackHosts == nil && empty(opts.RestHost) && empty(opts.RealtimeHost) && isDefaultPort {
		if opts.isProductionEnvironment() {
			return defaultOptions.FallbackHosts, nil
		}
		return getEnvFallbackHosts(opts.Environment), nil
	}
	return opts.FallbackHosts, nil
}

func (opts *ClientOptions) httpclient() *http.Client {
	if opts.HTTPClient != nil {
		return opts.HTTPClient
	}
	return &http.Client{
		Timeout: defaultOptions.HTTPRequestTimeout,
	}
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
