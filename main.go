// main.go
// Professional Single-File HTTP Todo Server in Go
// Author: bocaletto-luca
// License: MIT
//
// A modern, in-memory REST API with health, version, metrics, logging & graceful shutdown.
//
// Build & Run:
//   go run main.go              # default port 8080
//   go build -o todosrv .       # build binary
//   ./todosrv -port=9090        # listen on :9090

package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "sync"
    "time"
)

const version = "1.0.0"

// Todo represents a task.
type Todo struct {
    ID        int    `json:"id"`
    Title     string `json:"title"`
    Completed bool   `json:"completed"`
}

// Store holds todos in memory.
type Store struct {
    sync.RWMutex
    todos map[int]*Todo
    next  int
}

// NewStore initializes an empty store.
func NewStore() *Store {
    return &Store{todos: make(map[int]*Todo), next: 1}
}

func (s *Store) List() []*Todo {
    s.RLock()
    defer s.RUnlock()
    list := make([]*Todo, 0, len(s.todos))
    for _, t := range s.todos {
        list = append(list, t)
    }
    return list
}

func (s *Store) Create(title string) *Todo {
    s.Lock()
    defer s.Unlock()
    t := &Todo{ID: s.next, Title: title}
    s.todos[s.next] = t
    s.next++
    return t
}

func (s *Store) Get(id int) (*Todo, bool) {
    s.RLock()
    defer s.RUnlock()
    t, ok := s.todos[id]
    return t, ok
}

func (s *Store) Update(id int, title string, completed bool) (*Todo, bool) {
    s.Lock()
    defer s.Unlock()
    t, ok := s.todos[id]
    if !ok {
        return nil, false
    }
    t.Title = title
    t.Completed = completed
    return t, true
}

func (s *Store) Delete(id int) bool {
    s.Lock()
    defer s.Unlock()
    if _, ok := s.todos[id]; !ok {
        return false
    }
    delete(s.todos, id)
    return true
}

// Metrics collects basic stats.
type Metrics struct {
    sync.Mutex
    Requests   int `json:"requests"`
    TotalTodos int `json:"total_todos"`
}

func (m *Metrics) Inc() {
    m.Lock()
    m.Requests++
    m.Unlock()
}

func (m *Metrics) Snapshot(store *Store) map[string]int {
    m.Lock()
    defer m.Unlock()
    store.RLock()
    m.TotalTodos = len(store.todos)
    store.RUnlock()
    return map[string]int{"requests": m.Requests, "total_todos": m.TotalTodos}
}

// statusWriter captures HTTP status code.
type statusWriter struct {
    http.ResponseWriter
    status int
}

func (w *statusWriter) WriteHeader(code int) {
    w.status = code
    w.ResponseWriter.WriteHeader(code)
}

// withLogging logs method, path, status, duration.
func withLogging(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        lw := &statusWriter{w, http.StatusOK}
        next.ServeHTTP(lw, r)
        log.Printf("%s %s %d %v", r.Method, r.URL.Path, lw.status, time.Since(start))
    })
}

// withMetrics increments request counter.
func withMetrics(m *Metrics, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        m.Inc()
        next.ServeHTTP(w, r)
    })
}

func main() {
    port := flag.Int("port", 8080, "server port")
    flag.Parse()

    store := NewStore()
    metrics := &Metrics{}

    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })
    mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(version))
    })
    mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
        js, _ := json.MarshalIndent(metrics.Snapshot(store), "", "  ")
        w.Header().Set("Content-Type", "application/json")
        w.Write(js)
    })
    mux.HandleFunc("/todos", func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            respondJSON(w, store.List(), http.StatusOK)
        case http.MethodPost:
            var payload struct{ Title string }
            if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || strings.TrimSpace(payload.Title) == "" {
                http.Error(w, "invalid payload", http.StatusBadRequest)
                return
            }
            t := store.Create(payload.Title)
            respondJSON(w, t, http.StatusCreated)
        default:
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        }
    })
    mux.HandleFunc("/todos/", func(w http.ResponseWriter, r *http.Request) {
        idStr := strings.TrimPrefix(r.URL.Path, "/todos/")
        id, err := strconv.Atoi(idStr)
        if err != nil {
            http.Error(w, "invalid id", http.StatusBadRequest)
            return
        }
        switch r.Method {
        case http.MethodGet:
            if t, ok := store.Get(id); ok {
                respondJSON(w, t, http.StatusOK)
            } else {
                http.Error(w, "not found", http.StatusNotFound)
            }
        case http.MethodPut:
            var payload struct {
                Title     string `json:"title"`
                Completed bool   `json:"completed"`
            }
            if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
                http.Error(w, "invalid payload", http.StatusBadRequest)
                return
            }
            if t, ok := store.Update(id, payload.Title, payload.Completed); ok {
                respondJSON(w, t, http.StatusOK)
            } else {
                http.Error(w, "not found", http.StatusNotFound)
            }
        case http.MethodDelete:
            if store.Delete(id) {
                w.WriteHeader(http.StatusNoContent)
            } else {
                http.Error(w, "not found", http.StatusNotFound)
            }
        default:
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        }
    })

    handler := withLogging(withMetrics(metrics, mux))
    server := &http.Server{
        Addr:    fmt.Sprintf(":%d", *port),
        Handler: handler,
    }

    // Graceful shutdown
    idle := make(chan struct{})
    go func() {
        c := make(chan os.Signal, 1)
        signal.Notify(c, os.Interrupt)
        <-c
        log.Println("🔌 Shutdown signal received")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        server.Shutdown(ctx)
        close(idle)
    }()

    log.Printf("🚀 Server v%s listening on :%d", version, *port)
    if err := server.ListenAndServe(); err != http.ErrServerClosed {
        log.Fatalf("Server error: %v", err)
    }
    <-idle
    log.Println("👋 Goodbye")
}

func respondJSON(w http.ResponseWriter, data interface{}, code int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    json.NewEncoder(w).Encode(data)
}
