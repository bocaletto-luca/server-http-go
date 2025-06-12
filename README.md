# Go Todo HTTP Server 🚀

A single‐file, in‐memory RESTful API written in Go.  
Features health, version, metrics endpoints, request logging, and graceful shutdown.

**File:** `main.go`  
**Author:** [bocaletto-luca](https://github.com/bocaletto-luca)  
**License:** MIT

---

## 📦 Build & Run

## bash
#### Run with Go
    go run main.go
# Build binary
    go build -o todosrv .
# Custom port
    ./todosrv -port=9090

By default the server listens on :8080.
🔌 Endpoints

## Method	  Path	        Description
    GET	      /healthz	    Health check (200 “ok”)
    GET	      /version	    Server version
    GET	      /metrics	    JSON { requests, total_todos }
    GET	      /todos	      List all todos
    POST	    /todos	      Create todo { "title": "..." } → 201 Created
    GET	      /todos/{id}	  Get single todo
    PUT	      /todos/{id}	  Update { "title":"...", "completed":true }
    DELETE	  /todos/{id}	  Delete todo → 204 No Content

🛠️ Features

    In-memory store (no external DB)

    Thread-safe sync.RWMutex for concurrency

    Automatic JSON (un)marshalling

    Request logging: method, path, status, duration

    Basic metrics: total requests & todos count

    Graceful shutdown on SIGINT
