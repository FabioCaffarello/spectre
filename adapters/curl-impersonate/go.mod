module github.com/FabioCaffarello/spectre/adapters/curl-impersonate

go 1.26.0

// The generated protocol bindings live under proto/gen/go (gitignored,
// produced by `just proto-generate`). Consume them via a local replace
// directive — see ADR-0007.
require github.com/FabioCaffarello/spectre/proto/gen/go v0.0.0-00010101000000-000000000000

require (
	github.com/PuerkitoBio/goquery v1.12.0
	github.com/alicebob/miniredis/v2 v2.37.0
	github.com/antchfx/htmlquery v1.3.6
	github.com/go-redis/redismock/v9 v9.2.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.19.0
	golang.org/x/net v0.53.0
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/antchfx/xpath v1.3.6 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
)

replace github.com/FabioCaffarello/spectre/proto/gen/go => ../../proto/gen/go
