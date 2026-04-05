package session

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/storage"
)

type Session struct {
	ID        string
	Title     string
	Memory    string
	Directory string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func GetOrCreate(store *storage.SQLiteStore, dir string) (*Session, error) {
	// Try to find existing session for this directory
	row, err := store.FindSessionByDir(dir)
	if err == nil {
		return &Session{
			ID:        row.ID,
			Title:     row.Title,
			Memory:    row.Memory,
			Directory: row.Directory,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		}, nil
	}

	if err != sql.ErrNoRows {
		// Unexpected error, but still create a new session
	}

	// Create new session
	sess := &Session{
		ID:        uuid.New().String(),
		Title:     "",
		Memory:    "",
		Directory: dir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := store.CreateSession(&storage.SessionRow{
		ID:        sess.ID,
		Title:     sess.Title,
		Memory:    sess.Memory,
		Directory: sess.Directory,
	}); err != nil {
		return nil, err
	}

	return sess, nil
}

func NewSession(store *storage.SQLiteStore, dir string) (*Session, error) {
	sess := &Session{
		ID:        uuid.New().String(),
		Title:     "",
		Memory:    "",
		Directory: dir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := store.CreateSession(&storage.SessionRow{
		ID:        sess.ID,
		Title:     sess.Title,
		Memory:    sess.Memory,
		Directory: sess.Directory,
	}); err != nil {
		return nil, err
	}

	return sess, nil
}

// SaveMessage persists a message to storage.
func SaveMessage(store *storage.SQLiteStore, sessionID, role string, content []interface{}, usage *provider.Usage) error {
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return err
	}

	msg := &storage.MessageRow{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      role,
		Content:   contentJSON,
	}

	if usage != nil {
		msg.UsageInput = usage.InputTokens
		msg.UsageOutput = usage.OutputTokens
	}

	return store.AddMessage(msg)
}

// LoadMessages retrieves all messages for a session.
// It validates the message sequence and trims any incomplete tool exchanges
// (e.g., a tool_use without a matching tool_result from an interrupted session).
func LoadMessages(store *storage.SQLiteStore, sessionID string) ([]provider.Message, error) {
	rows, err := store.GetMessages(sessionID)
	if err != nil {
		return nil, err
	}

	var messages []provider.Message
	for _, row := range rows {
		var content []interface{}
		if err := json.Unmarshal(row.Content, &content); err != nil {
			continue
		}
		messages = append(messages, provider.Message{
			Role:    row.Role,
			Content: content,
		})
	}

	// Trim trailing incomplete tool exchanges.
	// The API requires every tool_use block to have a corresponding tool_result
	// in the immediately following message.
	messages = trimIncompleteToolUse(messages)

	return messages, nil
}

func ReplaceMessages(store *storage.SQLiteStore, sessionID string, messages []provider.Message) error {
	rows := make([]storage.MessageRow, 0, len(messages))
	for _, msg := range messages {
		contentJSON, err := json.Marshal(msg.Content)
		if err != nil {
			return err
		}
		rows = append(rows, storage.MessageRow{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Role:      msg.Role,
			Content:   contentJSON,
		})
	}
	return store.ReplaceMessages(sessionID, rows)
}

// trimIncompleteToolUse removes trailing messages that end with a tool_use
// block without a matching tool_result. This happens when a session is
// interrupted mid-execution.
func trimIncompleteToolUse(messages []provider.Message) []provider.Message {
	for len(messages) > 0 {
		last := messages[len(messages)-1]

		// If the last message is an assistant message with tool_use blocks,
		// check if there's a following tool_result (there won't be since it's last)
		if last.Role == "assistant" && hasToolUse(last.Content) {
			messages = messages[:len(messages)-1]
			continue
		}

		// If the last message is a user message with only tool_result blocks
		// but there's no following assistant message to continue the loop,
		// that's actually fine — the agent will pick up from here.
		break
	}

	// Also ensure messages alternate correctly: first message should be user
	if len(messages) > 0 {
		first := messages[0]
		if first.Role != "user" {
			// Drop leading assistant messages
			for len(messages) > 0 && messages[0].Role != "user" {
				messages = messages[1:]
			}
		}
	}

	return messages
}

// hasToolUse checks if any content block in a message is a tool_use block.
// Handles both concrete types and map[string]interface{} from JSON roundtrip.
func hasToolUse(content []interface{}) bool {
	for _, block := range content {
		switch b := block.(type) {
		case provider.ToolUseBlock:
			return true
		case map[string]interface{}:
			if t, _ := b["type"].(string); t == "tool_use" {
				return true
			}
		}
	}
	return false
}
