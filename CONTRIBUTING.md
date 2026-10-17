# Contributing

Thanks for helping. This file lists the rules a pull request must follow.

## Run the tests

```sh
go vet ./...
go test -race ./...
```

CI runs the same two commands. The race detector needs cgo, which the standard Go toolchain
has on Linux and macOS.

## Runtime floor

The floor is Go 1.21 (the `go` line in `go.mod`). CI runs the tests on Go 1.21 and on the
current stable Go. Do not use a language feature or a standard library API that Go 1.21 does
not have. The module has no dependencies outside the standard library. Keep it that way.

## Test fixtures

- `testdata/merchant-api-contract.json` is the shared response contract. Every Dominaite SDK
  carries the same bytes. Do not edit it in a pull request here. It changes only after the API
  changes.
- `testdata/merchant-api-wire-contract.json` is generated from the live production contract by
  `.github/workflows/contract-drift.yml`. Do not edit it by hand. When the drift check fails,
  the API moved: update the SDK first, then regenerate the fixture with the jq projection in
  that workflow.
- When a fixture test fails, fix the SDK, not the fixture.

## Tests with every change

Every change needs a test. Test what a caller gets back, not how the code works inside. A bug
fix needs a test that fails on the old code.

## Versions

The SDK is 0.x on purpose. A minor release (0.6 to 0.7) can contain a breaking change. A
breaking change goes in `CHANGELOG.md` under the next unreleased minor version. Do not change
the `Version` constant in `client.go` in a pull request.

## Releases

The maintainer tags releases. A pull request never creates a tag or a GitHub release.

## Security

Do not report a security problem in a public issue or pull request. See
[SECURITY.md](SECURITY.md).
