package app

import "github.com/abilisoft/usbip-go/pkg/domain"

// PublishSessionEventForTest exposes the internal session-event fan-out
// so race-detector tests can drive the publish path directly and
// reproduce the publish-vs-unsubscribe window.
func PublishSessionEventForTest(e *Exporter, ev domain.Event) {
	e.publishSessionEvent(ev)
}
