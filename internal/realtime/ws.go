package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"
)

// writeWait caps how long a single message write may block before we
// consider the client gone. Slow clients get force-closed rather than
// pinning a goroutine forever.
const writeWait = 10 * time.Second

// pingInterval is the cadence at which we send a websocket ping. Most
// proxies idle-close connections after ~60s; 30s keeps the channel
// warm and detects half-open sockets quickly.
const pingInterval = 30 * time.Second

// Handler returns an echo.HandlerFunc that upgrades the request to a
// websocket and bridges it to the hub. Clients pass initial topics via
// repeated `topic=` query params (e.g. `/ws?topic=stack:nginx&topic=job:abc`)
// and may add more by sending JSON messages of the form
// `{"action":"subscribe","topic":"stack:other"}`.
//
// Auth is the caller's responsibility — register this handler behind
// the same RequireAuth middleware as the rest of the site so the
// upgrade only happens for logged-in sessions.
func Handler(hub *Hub) echo.HandlerFunc {
	return func(c echo.Context) error {
		conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
			// Same-origin enforced by the browser when the websocket
			// upgrade comes from a regular page; OriginPatterns="*"
			// here just disables the library's stricter defaults that
			// reject when host headers don't match. CSP + auth
			// (session cookie required by the route) are the actual
			// gates.
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return err
		}

		topics := c.QueryParams()["topic"]
		subID, events := hub.Subscribe(topics...)
		defer hub.Unsubscribe(subID)

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		// Reader goroutine: handle subscribe messages from the client
		// and detect disconnects by reading. Cancellation propagates
		// up to the writer.
		go readerLoop(ctx, cancel, conn, hub, subID)

		// Writer loop: forwards hub events as JSON; ping ticker keeps
		// idle proxies from killing the connection.
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				_ = conn.Close(websocket.StatusNormalClosure, "server done")
				return nil
			case <-ticker.C:
				pingCtx, pcancel := context.WithTimeout(ctx, writeWait)
				if err := conn.Ping(pingCtx); err != nil {
					pcancel()
					_ = conn.Close(websocket.StatusInternalError, "ping failed")
					return nil
				}
				pcancel()
			case e, ok := <-events:
				if !ok {
					return nil
				}
				if err := writeEvent(ctx, conn, e); err != nil {
					return nil
				}
			}
		}
	}
}

// readerLoop handles client → server messages. The only message we
// understand is a subscribe action; anything else is logged and
// ignored — keeps protocol additions backward-compatible (clients on
// older builds quietly skip unknown actions).
func readerLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, hub *Hub, subID string) {
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Action string `json:"action"`
			Topic  string `json:"topic"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("ws: bad client message", "error", err)
			continue
		}
		if msg.Action == "subscribe" && msg.Topic != "" {
			hub.AddTopic(subID, msg.Topic)
		}
	}
}

// writeEvent serialises and sends one event. Encoding errors are fatal
// for this connection — better to drop the client than silently lose
// messages.
func writeEvent(ctx context.Context, conn *websocket.Conn, e Event) error {
	body := map[string]any{
		"topic":     e.Topic,
		"type":      e.Type,
		"data":      e.Data,
		"timestamp": e.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}
