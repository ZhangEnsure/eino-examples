/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package mem 提供基于 JSONL 文件的对话历史持久化存储。
//
// 这是业务层概念，不是 Eino 框架的核心组件。
// Eino 框架只负责"如何处理消息"，而"如何存储消息"完全由业务层决定。
//
// 核心概念：
//   - Session：一次完整的对话会话，包含 ID、创建时间、消息列表
//   - Store：管理多个 Session 的持久化存储，支持创建、获取、列表、删除
//   - JSONL 格式：每行一个 JSON 对象，第一行为 session header，后续行为消息
package mem

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// SessionMeta 提供 Session 的摘要信息，用于列表展示。
type SessionMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// Session 持有单次对话的内存状态，并支持持久化到 JSONL 文件。
//
// 文件格式：
//
//	{"type":"session","id":"...","created_at":"..."}   ← header (第 1 行)
//	{"role":"user","content":"..."}                    ← message (第 2 行起)
type Session struct {
	ID        string
	CreatedAt time.Time

	filePath string
	mu       sync.Mutex
	messages []*schema.Message
}

// Append 追加一条消息到会话，并持久化到磁盘。
func (s *Session) Append(msg *schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, msg)

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// GetMessages 返回所有消息的快照副本。
func (s *Session) GetMessages() []*schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*schema.Message, len(s.messages))
	copy(result, s.messages)
	return result
}

// Title 从第一条用户消息生成会话标题。
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, msg := range s.messages {
		if msg.Role == schema.User && msg.Content != "" {
			title := msg.Content
			if len([]rune(title)) > 60 {
				title = string([]rune(title)[:60]) + "..."
			}
			return title
		}
	}
	return "New Session"
}

// MessageCount 返回当前会话中的消息数量。
func (s *Session) MessageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Store 管理多个 Session 的持久化存储，使用 JSONL 文件作为后端。
type Store struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*Session
}

// NewStore 创建一个新的 Store，使用指定目录存储 Session 文件。
// 如果目录不存在会自动创建。
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建 session 目录失败: %w", err)
	}
	return &Store{
		dir:   dir,
		cache: make(map[string]*Session),
	}, nil
}

// GetOrCreate 获取指定 ID 的 Session，如果不存在则创建新的。
func (s *Store) GetOrCreate(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.cache[id]; ok {
		return sess, nil
	}

	filePath := filepath.Join(s.dir, id+".jsonl")

	var (
		sess *Session
		err  error
	)
	if _, statErr := os.Stat(filePath); os.IsNotExist(statErr) {
		sess, err = createSession(id, filePath)
	} else {
		sess, err = loadSession(filePath)
	}
	if err != nil {
		return nil, err
	}

	s.cache[id] = sess
	return sess, nil
}

// List 返回所有已知 Session 的元数据。
func (s *Store) List() ([]SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}

	var metas []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")

		if sess, ok := s.cache[id]; ok {
			metas = append(metas, SessionMeta{ID: id, Title: sess.Title(), CreatedAt: sess.CreatedAt})
			continue
		}

		sess, loadErr := loadSession(filepath.Join(s.dir, e.Name()))
		if loadErr != nil {
			continue
		}
		metas = append(metas, SessionMeta{ID: id, Title: sess.Title(), CreatedAt: sess.CreatedAt})
	}
	return metas, nil
}

// Delete 删除指定 Session 的文件并从缓存中移除。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := filepath.Join(s.dir, id+".jsonl")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.cache, id)
	return nil
}

// sessionHeader 是每个 Session JSONL 文件的第一行。
type sessionHeader struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// createSession 创建一个新的 Session，写入 header 到文件。
func createSession(id, filePath string) (*Session, error) {
	header := sessionHeader{
		Type:      "session",
		ID:        id,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filePath, append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	return &Session{
		ID:        id,
		CreatedAt: header.CreatedAt,
		filePath:  filePath,
		messages:  make([]*schema.Message, 0),
	}, nil
}

// loadSession 从 JSONL 文件加载一个已有的 Session。
func loadSession(filePath string) (*Session, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 设置较大的缓冲区，避免长消息行被截断
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// 第一行：session header
	if !scanner.Scan() {
		return nil, fmt.Errorf("空的 session 文件: %s", filePath)
	}
	var header sessionHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return nil, fmt.Errorf("session header 解析失败 %s: %w", filePath, err)
	}

	sess := &Session{
		ID:        header.ID,
		CreatedAt: header.CreatedAt,
		filePath:  filePath,
		messages:  make([]*schema.Message, 0),
	}

	// 后续行：消息
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg schema.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // 跳过格式错误的行
		}
		sess.messages = append(sess.messages, &msg)
	}

	return sess, scanner.Err()
}
