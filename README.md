# usbip-go

Pure-Go reimplementation of USB/IP userspace — library (`pkg/usbip`), client CLI (`usbip`), and server daemon (`usbipd`) for Linux.

## Status

Early development. Not yet functional; APIs and on-disk artifacts are unstable.

## Prerequisites

- Go 1.26 or newer.
- The [Task](https://taskfile.dev) runner for the repository's build/test targets. Bootstrap it with:

  ```
  go install github.com/go-task/task/v3/cmd/task@latest
  ```
