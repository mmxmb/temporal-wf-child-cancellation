1. Start Temporal server: `temporal server start-dev`
2. Start worker: `go run worker/main.go`
3. Start parent workflow and cancel it shortly after: `go run starter/main.go`
