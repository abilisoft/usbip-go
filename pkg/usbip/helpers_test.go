package usbip_test

import "errors"

// errNotImplemented is returned by stub methods whose behaviour a
// particular test never exercises. A real call would surface this
// sentinel, making the gap loud rather than silent.
var errNotImplemented = errors.New("stub: not implemented")
