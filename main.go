// main.go
// Professional Single-File HTTP Todo Server in Go
// Author: bocaletto-luca
// License: MIT
//
// A modern RESTful API for managing “Todo” items in memory,
// with health, metrics, logging and graceful shutdown.
// Build & Run:
//   go run main.go           # defaults to :8080
//   go build -o todosrv .    # creates binary
//   ./todosrv -port=9090     # listen on :9090
//
// Endpoints:
//   GET    /healthz              → 200 OK “ok”
//   GET    /metrics              → JSON with request & todo stats
//   GET    /todos                → list all todos
//   POST   /todos                → create a todo { "title": "..." }
//   GET    /todos/{id}           → get a todo
//   PUT    /todos/{id}           → update a todo { "title": "...", "completed": true }
//   DELETE /todos/{id}           → delete a todo

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

type Todo struct {
    ID        int    `json:"id"`
    Title     string `json:"title"`
    Completed bool   `json:"completed"`
}

type Store struct {
    sync.RWMutex
    todos map[int]*Todo
    next  int
}

func NewStore() *Store {
    return &Store{todos: make(map[int]*Todo), next: 1}
}

func (s *Store) List() []*Todo {
    s.RLock(); defer s.RUnlock()
    out := make([]*Todo, 0, len(s.todos))
    for _, t := range s.todos {
        out = append(out, t)
    }
    return out
}

func (s *Store) Create(title string) *Todo {
    s.Lock(); defer s.Unlock()
    t := &Todo{ID: s.next, Title: title}
    s.todos[s.next] = t
    s.next++
    return t
}

func (s *Store) Get(id int) (*Todo, bool) {
    s.RLock(); defer s.RUnlock()
    t, ok := s.todos[id]
    return t, ok
}

func (s *Store) Update(id int, title string, completed bool) (*Todo, bool) {
    s.Lock(); defer s.Unlock()
    t, ok := s.todos[id]
    if !ok {
        return nil, false
    }
    t.Title = title
    t.Completed = completed
    return t, true
}

func (s *Store) Delete(id int) bool {
    s.Lock(); defer s.Unlock()
    if _, ok := s.todos[id]; !ok {
        return false
    }
    delete(s.todos, id)
    return true
}

type Metrics struct {
    sync.Mutex
    Requests     int `json:"requests"`
    TotalTodos   int `json:"total_todos"`
    ActiveClients int `json:"-"`
}

func (m *Metrics) IncRequests() {
    m.Lock(); m.Requests++; m.Unlock()
}

func (m *Metrics) Snapshot(store *Store) map[string]int {
    m.Lock(); defer m.Unlock()
    store.RLock(); defer store.RUnlock()
    m.TotalTodos = len(store.todos)
    return map[string]int{
        "requests":   m.Requests,
        "total_todos": m.TotalTodos,
    }
}

// logging middleware
func withLogging(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        lw := &statusWriter{w, http.StatusOK}
        next.ServeHTTP(lw, r)
        log.Printf("%s %s %d %v", r.Method, r.URL.Path, lw.status, time.Since(start))
    })
}

// metrics middleware
func withMetrics(m *Metrics, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        m.IncRequests()
        next.ServeHTTP(w, r)
    })
}

type statusWriter struct {
    http.ResponseWriter
    status int
}

func (w *statusWriter) WriteHeader(code int) {
    w.status = code
    w.ResponseWriter.WriteHeader(code)
}

func main() {
    port := flag.Int("port", 8080, "server port")
    flag.Parse()

    store := NewStore()
    metrics := &Metrics{}

    mux := http.NewServeMux()
    mux.Handle("/healthz", http.HandlerFunc(healthHandler))
    mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        js, _ := json.MarshalIndent(metrics.Snapshot(store), "", "  ")
        w.Header().Set("Content-Type", "application/json")
        w.Write(js)
    }))
    mux.Handle("/todos", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            all := store.List()
            respondJSON(w, all, http.StatusOK)
        case http.MethodPost:
            var inp struct{ Title string }
            if err := json.NewDecoder(r.Body).Decode(&inp); err != nil || strings.TrimSpace(inp.Title) == "" {
                http.Error(w, "invalid payload", http.StatusBadRequest)
                return
            }
            t := store.Create(inp.Title)
            respondJSON(w, t, http.StatusCreated)
        default:
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        }
    }))
    mux.Handle("/todos/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
            var inp struct {
                Title     string `json:"title"`
                Completed bool   `json:"completed"`
            }
            if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
                http.Error(w, "invalid payload", http.StatusBadRequest)
                return
            }
            if t, ok := store.Update(id, inp.Title, inp.Completed); ok {
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
    }))

    handler := withLogging(withMetrics(metrics, mux))
    srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: handler}

    // Graceful shutdown
    idleConnsClosed := make(chan struct{})
    go func() {
        c := make(chan os.Signal, 1)
        signal.Notify(c, os.Interrupt)
        <-c
        log.Println("🔌 Shutdown signal received")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        srv.Shutdown(ctx)
        close(idleConnsClosed)
    }()

    log.Printf("🚀 Server v%s listening on :%d", version, *port)
    if err := srv.ListenAndServe(); err != http.ErrServerClosed {
        log.Fatalf("Server error: %v", err)
    }
    <-idleConnsClosed
    log.Println("👋 Goodbye")
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
}

func respondJSON(w http.ResponseWriter, data interface{}, code int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    json.NewEncoder(w).Encode(data)
}
