module github.com/FabioCaffarello/spectre/infra-services/proxy-broker

go 1.26.0

replace github.com/FabioCaffarello/spectre/proto/gen/go => ../../proto/gen/go

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.19.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)
