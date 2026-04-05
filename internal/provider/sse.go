package provider

import (
	"bufio"
	"io"
	"strings"
)

// SSEEvent is a parsed Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
}

// ParseSSE reads SSE events from a reader and sends them on a channel.
func ParseSSE(r io.Reader) <-chan SSEEvent {
	ch := make(chan SSEEvent, 16)

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)

		var event SSEEvent
		var dataLines []string

		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				// Empty line = end of event
				if len(dataLines) > 0 {
					event.Data = strings.Join(dataLines, "\n")
					ch <- event
				}
				event = SSEEvent{}
				dataLines = nil
				continue
			}

			if strings.HasPrefix(line, "event: ") {
				event.Event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			} else if line == "data:" {
				dataLines = append(dataLines, "")
			}
		}

		// Flush any remaining event
		if len(dataLines) > 0 {
			event.Data = strings.Join(dataLines, "\n")
			ch <- event
		}
	}()

	return ch
}
