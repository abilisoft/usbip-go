# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and entries are generated from [Conventional Commits](https://www.conventionalcommits.org/) via `git-cliff`.

## [Unreleased]

### Bug Fixes

- **domain:** Address codex Phase 1 findings (fd98827)
- **taskfile:** Enable cgo for -race test targets (d7e3f5f)

### CI & Build

- **integration:** Add go-build cache and kernel-module preflight (70f61b8)
- Schedule mutation, fix api baseline paths, harden no-cgo grep (b675ba2)
- Add lint/test/vuln matrix + TDD + api/boundary/cgo/coverage/cross-compile guards (01be6cb)

### Chores

- **tools:** Pin apidiff and goimports, install via -mod=mod (f62b9ed)
- Add pre-commit hook running fmt/lint/test (6c5979b)
- Scaffold package doc.go files (55f8c42)
- Pin dev tools via tools.go (10d3ea6)
- Add strict golangci-lint config (7d4c528)
- Add Taskfile with build/test/lint/release targets (e451118)
- Init go module (4a87213)

### Documentation

- **taskfile:** Clarify cgo policy comment (4ef95d7)
- Sync .gitignore with updated plan template (9aa506f)
- Add initial design spec and implementation plan (fa415e2)

### Features

- **domain:** Add sentinel errors per spec §4.4 and §6.2 (0f3e7e3)
- **domain:** Add Event interface and nine concrete event structs (fb92895)
- **domain:** Add Session/SessionID with UUIDv7 generation (e9ce4f1)
- **domain:** Add Port/PortID per spec §4.2 (02c3991)
- **domain:** Add Device/Interface per spec §4.2 (74886c3)
- **domain:** Add RemoteEndpoint with ParseRemote (efb7abd)
- **domain:** Add USBClass/Subclass/Protocol with usb.ids subset (c11cb84)
- **domain:** Add Status enum with String() (475dad7)
- **domain:** Add Speed enum with String() (9c9d849)
- **domain:** Add DeviceID with BusNum/DevNum accessors (42c4a04)
- **domain:** Add BusID value object with ParseBusID (1cc7cf6)
- **domain:** Add constants per spec §4.5 (d3cfc10)

### Tests

- **domain:** Reject malformed busid topology and invalid remote hosts (6efa3d7)
- **domain:** Close coverage gaps to 99% (11fcbec)
- **domain:** Add sentinel error tests (7b05f0e)
- **domain:** Add Event interface and kind tests (34ac1b5)
- **domain:** Add Session/SessionID UUIDv7 tests (9415962)
- **domain:** Add RemoteEndpoint tests (a749eb1)
- **domain:** Add USBClass/Subclass/Protocol tests (829e4ad)
- **domain:** Add Status enum tests (546c67d)
- **domain:** Add Speed enum tests (d341be4)
- **domain:** Add DeviceID bit accessor tests (82e9815)
- **domain:** Add BusID value object tests (cc946e2)
- **domain:** Add constants golden test (19c0c33)

