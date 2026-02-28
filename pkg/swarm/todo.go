// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"encoding/json"
	"sync"
	"time"
)

// TodoItem represents a single todo item.
type TodoItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Desc        string    `json:"description,omitempty"`
	Priority    int       `json:"priority"` // 0=low, 1=medium, 2=high
	Status      string    `json:"status"`   // "pending", "in_progress", "completed", "canceled"
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	DueAt       time.Time `json:"due_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
}

// TodoList manages a list of todo items for a node.
type TodoList struct {
	items     []*TodoItem
	mu        sync.RWMutex
	nodeID    string
	discovery *DiscoveryService // For publishing updates
}

// NewTodoList creates a new todo list for a node.
func NewTodoList(nodeID string) *TodoList {
	return &TodoList{
		items:  make([]*TodoItem, 0),
		nodeID: nodeID,
	}
}

// SetDiscovery sets the discovery service for publishing updates.
func (tl *TodoList) SetDiscovery(ds *DiscoveryService) {
	tl.discovery = ds
}

// Add adds a new todo item.
func (tl *TodoList) Add(title string, priority int) *TodoItem {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	now := time.Now()
	item := &TodoItem{
		ID:        generateTodoID(),
		Title:     title,
		Priority:  priority,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}

	tl.items = append(tl.items, item)
	tl.publishIfNeeded()
	return item
}

// Update updates an existing todo item.
func (tl *TodoList) Update(id string, updates func(*TodoItem)) bool {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	for _, item := range tl.items {
		if item.ID == id {
			updates(item)
			item.UpdatedAt = time.Now()
			tl.publishIfNeeded()
			return true
		}
	}
	return false
}

// Remove removes a todo item by ID.
func (tl *TodoList) Remove(id string) bool {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	for i, item := range tl.items {
		if item.ID == id {
			tl.items = append(tl.items[:i], tl.items[i+1:]...)
			tl.publishIfNeeded()
			return true
		}
	}
	return false
}

// Get retrieves a todo item by ID.
func (tl *TodoList) Get(id string) *TodoItem {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	for _, item := range tl.items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

// List returns all todo items.
func (tl *TodoList) List() []*TodoItem {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	result := make([]*TodoItem, len(tl.items))
	copy(result, tl.items)
	return result
}

// ListByStatus returns todo items filtered by status.
func (tl *TodoList) ListByStatus(status string) []*TodoItem {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	result := make([]*TodoItem, 0)
	for _, item := range tl.items {
		if item.Status == status {
			result = append(result, item)
		}
	}
	return result
}

// ListByPriority returns todo items sorted by priority (highest first).
func (tl *TodoList) ListByPriority() []*TodoItem {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	result := make([]*TodoItem, len(tl.items))
	copy(result, tl.items)

	// Simple bubble sort by priority (descending)
	for i := 0; i < len(result)-1; i++ {
		for j := 0; j < len(result)-i-1; j++ {
			if result[j].Priority < result[j+1].Priority {
				result[j], result[j+1] = result[j+1], result[j]
			}
		}
	}
	return result
}

// Complete marks a todo item as completed.
func (tl *TodoList) Complete(id string) bool {
	return tl.Update(id, func(item *TodoItem) {
		item.Status = "completed"
		item.CompletedAt = time.Now()
	})
}

// Start marks a todo item as in progress.
func (tl *TodoList) Start(id string) bool {
	return tl.Update(id, func(item *TodoItem) {
		item.Status = "in_progress"
	})
}

// Cancel marks a todo item as canceled.
func (tl *TodoList) Cancel(id string) bool {
	return tl.Update(id, func(item *TodoItem) {
		item.Status = "canceled"
	})
}

// Clear removes all completed or canceled items.
func (tl *TodoList) Clear() int {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	count := 0
	newItems := make([]*TodoItem, 0, len(tl.items))
	for _, item := range tl.items {
		if item.Status != "completed" && item.Status != "canceled" {
			newItems = append(newItems, item)
		} else {
			count++
		}
	}
	tl.items = newItems

	if count > 0 {
		tl.publishIfNeeded()
	}
	return count
}

// Count returns the total number of todo items.
func (tl *TodoList) Count() int {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return len(tl.items)
}

// CountByStatus returns the count of items by status.
func (tl *TodoList) CountByStatus() map[string]int {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	counts := make(map[string]int)
	for _, item := range tl.items {
		counts[item.Status]++
	}
	return counts
}

// ToStrings converts the todo list to a slice of strings for status reporting.
func (tl *TodoList) ToStrings() []string {
	tl.mu.RLock()
	defer tl.mu.RUnlock()

	result := make([]string, 0, len(tl.items))
	for _, item := range tl.items {
		prefix := ""
		switch item.Status {
		case "completed":
			prefix = "✅ "
		case "in_progress":
			prefix = "🔄 "
		case "canceled":
			prefix = "❌ "
		case "pending":
			if item.Priority == 2 {
				prefix = "🔴 "
			} else if item.Priority == 1 {
				prefix = "🟡 "
			} else {
				prefix = "⚪ "
			}
		}
		result = append(result, prefix+item.Title)
	}
	return result
}

// publishIfNeeded publishes the todo list to the discovery service if available.
func (tl *TodoList) publishIfNeeded() {
	if tl.discovery == nil {
		return
	}

	todoStrings := tl.ToStrings()
	tl.discovery.SetTodoList(todoStrings)
}

// generateTodoID generates a unique ID for a todo item.
func generateTodoID() string {
	return time.Now().Format("20060102-150405") + "-" + randomString(4)
}

// randomString generates a random string of given length.
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// TodoListSummary represents a summary of a node's todo list.
type TodoListSummary struct {
	NodeID       string `json:"node_id"`
	Total        int    `json:"total"`
	Pending      int    `json:"pending"`
	InProgress   int    `json:"in_progress"`
	Completed    int    `json:"completed"`
	Canceled     int    `json:"canceled"`
	HighPriority int    `json:"high_priority"`
	Timestamp    int64  `json:"timestamp"`
}

// Summary returns a summary of the todo list.
func (tl *TodoList) Summary() *TodoListSummary {
	counts := tl.CountByStatus()
	highPriority := 0
	for _, item := range tl.items {
		if item.Priority == 2 && item.Status != "completed" && item.Status != "canceled" {
			highPriority++
		}
	}

	return &TodoListSummary{
		NodeID:       tl.nodeID,
		Total:        tl.Count(),
		Pending:      counts["pending"],
		InProgress:   counts["in_progress"],
		Completed:    counts["completed"],
		Canceled:     counts["canceled"],
		HighPriority: highPriority,
		Timestamp:    time.Now().UnixNano(),
	}
}

// MarshalJSON implements json.Marshaler.
func (tl *TodoList) MarshalJSON() ([]byte, error) {
	return json.Marshal(tl.List())
}
