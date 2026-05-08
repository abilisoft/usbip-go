# UUIDv7 for SessionID

Sessions are identified by UUIDv7 rather than UUIDv4 or a sequential
integer. UUIDv7 encodes a millisecond-resolution timestamp in the high
bits, so sessions sort chronologically by ID alone — log correlation,
metric labelling, and status-UDS output all use the natural lexical
order without a separate `started_at` sort key.

UUIDv4 was the obvious default and was rejected because the random bit
layout makes correlation across log lines harder to scan by eye, and
because sorted queries (e.g. "show the last 20 sessions") require
fetching `started_at` separately. Sequential integers were rejected
because they leak session volume and require a shared counter that
would complicate multi-process or restart scenarios.

The timestamp in a UUIDv7 is millisecond-granularity; for the purposes
of session accounting this is adequate — sub-millisecond collision is
not a realistic concern for a daemon with a 128-session default cap.
