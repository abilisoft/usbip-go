package transport

// IsSockoptFatalForTest exposes isSockoptFatal so the package-external
// test can drive the classifier across the errno matrix without
// staging a real live-then-dead TCP connection. The production
// isSockoptFatal stays unexported because it has no use outside
// Dial's SetNoDelay path.
func IsSockoptFatalForTest(err error) bool {
	return isSockoptFatal(err)
}
