module github.com/FabioCaffarello/spectre/adapters/curl-impersonate

go 1.24.0

// The generated protocol bindings live under proto/gen/go (gitignored,
// produced by `just proto-generate`). Consume them via a local replace
// directive — see ADR-0007.
require github.com/FabioCaffarello/spectre/proto/gen/go v0.0.0-00010101000000-000000000000

require github.com/google/uuid v1.6.0

require (
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/FabioCaffarello/spectre/proto/gen/go => ../../proto/gen/go
