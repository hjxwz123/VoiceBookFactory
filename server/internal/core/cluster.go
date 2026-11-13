package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"voicebook/internal/bus"
	"voicebook/internal/events"
)

// Messages between the instances of a deployment (see package bus). With a single instance the
// bus is in-process and these round trips stay inside the process.
const (
	topicEvents = "events" // realtime events for connected browsers
	topicJobs   = "jobs"   // job wake-ups and cancellations
	topicCache  = "cache"  // drop a cached value everywhere
)

type eventMsg struct {
	Scope string       `json:"scope"` // studio | user
	ID    string       `json:"id"`
	Event events.Event `json:"event"`
}

type jobMsg struct {
	Kind string `json:"kind"` // wake | cancel
	Arg  string `json:"arg"`
}

type cacheMsg struct {
	Kind string `json:"kind"` // setting | tts-providers | queues
	Key  string `json:"key,omitempty"`
}

// SetBus replaces the in-process bus (call before Start), e.g. with Redis for several instances.
func (a *App) SetBus(b bus.Bus) { a.Bus = b }

// wireBus subscribes this instance to the deployment's messages and starts receiving them.
func (a *App) wireBus(ctx context.Context) {
	a.Bus.Subscribe(topicEvents, func(p []byte) {
		var m eventMsg
		if json.Unmarshal(p, &m) != nil {
			return
		}
		switch m.Scope {
		case "studio":
			a.Hub.Studio(m.ID, m.Event)
		case "user":
			a.Hub.User(m.ID, m.Event)
		}
	})
	a.Bus.Subscribe(topicJobs, func(p []byte) {
		var m jobMsg
		if json.Unmarshal(p, &m) != nil {
			return
		}
		switch m.Kind {
		case "wake":
			a.Q.Wake(m.Arg)
		case "cancel":
			a.Q.CancelLocal(m.Arg)
		}
	})
	a.Bus.Subscribe(topicCache, func(p []byte) {
		var m cacheMsg
		if json.Unmarshal(p, &m) != nil {
			return
		}
		switch m.Kind {
		case "setting":
			a.settings.Delete(m.Key)
		case "tts-providers":
			a.dropTTSProviders()
		case "queues":
			a.loadQueues()
		}
	})
	if a.Bus.Kind() != "local" { // a single instance already wakes and cancels in process
		a.Q.Broadcast = func(kind, arg string) { a.publish(topicJobs, jobMsg{Kind: kind, Arg: arg}) }
	}
	a.Bus.Start(ctx)
}

func (a *App) publish(topic string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("bus: cannot encode message", "topic", topic, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = a.Bus.Publish(ctx, topic, b) // a failed publish is still delivered on this instance
}

func stamp(ev events.Event) events.Event {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	return ev
}

// emitUser pushes an event to every connection of one user, on whichever instance it is.
func (a *App) emitUser(userID string, ev events.Event) {
	if userID == "" {
		return
	}
	a.publish(topicEvents, eventMsg{Scope: "user", ID: userID, Event: stamp(ev)})
}

// once reports whether this instance should run the scheduled task now: with several instances only
// one of them runs it per interval.
func (a *App) once(task string, every time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return a.Bus.Lock(ctx, "cron:"+task, every-5*time.Second)
}
