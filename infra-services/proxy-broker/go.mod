module github.com/FabioCaffarello/spectre/infra-services/proxy-broker

go 1.26.0

replace github.com/FabioCaffarello/spectre/proto/gen/go => ../../proto/gen/go

require (
	github.com/FabioCaffarello/spectre/proto/gen/go v0.0.0-00010101000000-000000000000
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.19.0
	google.golang.org/grpc v1.81.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)
