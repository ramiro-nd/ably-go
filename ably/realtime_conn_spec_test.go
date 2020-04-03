package ably_test

import (
	"fmt"
	"testing"

	"github.com/ably/ably-go/ably"
	"github.com/ably/ably-go/ably/ablytest"
)

func Test_RTN4a_ConnectionEventForStateChange(t *testing.T) {
	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateConnectingV12), func(t *testing.T) {
		t.Parallel()

		app, realtime := ablytest.NewRealtimeClient(&ably.ClientOptions{NoConnect: true})
		defer safeclose(t, realtime, app)

		changes := make(chan ably.ConnectionStateChangeV12)
		defer ablytest.Instantly.NoRecv(t, nil, changes, t.Errorf)

		realtime.Connection.OnV12(ably.ConnectionEventConnectingV12, func(change ably.ConnectionStateChangeV12) {
			changes <- change
		})

		realtime.ConnectV12()

		ablytest.Soon.Recv(t, nil, changes, t.Fatalf)
	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateConnectedV12), func(t *testing.T) {
		t.Parallel()

		app, realtime := ablytest.NewRealtimeClient(&ably.ClientOptions{NoConnect: true})
		defer safeclose(t, realtime, app)

		connectAndWait(t, realtime)
	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateDisconnectedV12), func(t *testing.T) {
		t.Parallel()

		options := &ably.ClientOptions{NoConnect: true}
		disconnect := ablytest.SetFakeDisconnect(options)
		app, realtime := ablytest.NewRealtimeClient(options)
		defer safeclose(t, app)
		defer realtime.CloseV12()

		connectAndWait(t, realtime)

		changes := make(chan ably.ConnectionStateChangeV12)
		defer ablytest.Instantly.NoRecv(t, nil, changes, t.Errorf)

		realtime.Connection.OnV12(ably.ConnectionEventDisconnectedV12, func(change ably.ConnectionStateChangeV12) {
			changes <- change
		})

		err := disconnect()
		if err != nil {
			t.Fatalf("fake disconnection failed: %v", err)
		}

		ablytest.Soon.Recv(t, nil, changes, t.Fatalf)

	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateSuspendedV12), func(t *testing.T) {
		t.Parallel()

		t.Fatalf("SUSPENDED not yet implemented")
	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateClosingV12), func(t *testing.T) {
		t.Parallel()

		app, realtime := ablytest.NewRealtimeClient(&ably.ClientOptions{NoConnect: true})
		defer safeclose(t, realtime, app)

		connectAndWait(t, realtime)

		changes := make(chan ably.ConnectionStateChangeV12)
		defer ablytest.Instantly.NoRecv(t, nil, changes, t.Errorf)

		realtime.Connection.OnV12(ably.ConnectionEventClosingV12, func(change ably.ConnectionStateChangeV12) {
			changes <- change
		})

		realtime.CloseV12()
		ablytest.Soon.Recv(t, nil, changes, t.Fatalf)
	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateClosedV12), func(t *testing.T) {
		t.Parallel()

		app, realtime := ablytest.NewRealtimeClient(&ably.ClientOptions{NoConnect: true})
		defer safeclose(t, realtime, app)

		connectAndWait(t, realtime)

		changes := make(chan ably.ConnectionStateChangeV12)
		defer ablytest.Instantly.NoRecv(t, nil, changes, t.Errorf)

		realtime.Connection.OnV12(ably.ConnectionEventClosedV12, func(change ably.ConnectionStateChangeV12) {
			changes <- change
		})

		realtime.CloseV12()
		ablytest.Soon.Recv(t, nil, changes, t.Fatalf)
	})

	t.Run(fmt.Sprintf("on %s", ably.ConnectionStateFailedV12), func(t *testing.T) {
		t.Parallel()
	})
}

func connectAndWait(t *testing.T, realtime *ably.RealtimeV12) {
	t.Helper()

	changes := make(chan ably.ConnectionStateChangeV12)
	defer ablytest.Instantly.NoRecv(t, nil, changes, t.Errorf)

	realtime.Connection.OnceV12(ably.ConnectionEventConnectedV12, func(change ably.ConnectionStateChangeV12) {
		changes <- change
	})

	realtime.ConnectV12()
	ablytest.Soon.Recv(t, nil, changes, t.Fatalf)
}
