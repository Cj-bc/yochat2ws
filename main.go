package main

import (
	"log/slog"
	"strings"
	"fmt"
	"log"
	"context"
	"net/http"
	"errors"
  "google.golang.org/api/youtube/v3"
  "google.golang.org/api/option"
	"google.golang.org/api/googleapi"
	"nhooyr.io/websocket"
)

type Command int
const (
	COM_BYE Command = iota
)

type Server struct {
	subs []*Subscriber
}

type Subscriber struct {
	nextPageToken string
	service *youtube.LiveChatMessagesService
}

func (s Subscriber) ChatMessages() []*youtube.LiveChatMessage {
	return []*youtube.LiveChatMessage{}
}

func (s Subscriber) Run() {
	// s.service.List()
}

// Retrive Live chat ID for given broadcastId
func RetriveLiveChatId(broadcastId string, service *youtube.LiveBroadcastsService) (string, error) {
	call := service.List([]string{"snippet"})
	
	if response, err := call.Do(); err != nil {
		return "", err
	} else if len(response.Items) == 0 {
		return "", fmt.Errorf("No response for broadcastId %v", broadcastId)
	} else {
		return response.Items[0].Snippet.LiveChatId, nil
	}
}

// Receive Command from peer and send it to channel 
func CommandReaderGoroutine(ctx context.Context, c *websocket.Conn, ch chan<- Command) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if _type, msg, err := c.Read(ctx); err != nil {
				log.Printf("Failed to read message from socket: %v", err)
			} else if _type != websocket.MessageText {
				log.Printf("Wrong message type: %v", _type)
			} else if string(msg) == "BYE" {
				ch <- COM_BYE
				return
			}
		}
	}
}

func ReceiveMessages(ctx context.Context, service *youtube.LiveChatMessagesService, chatId string, ch chan<- *youtube.LiveChatMessage) error {
	call := service.List(chatId, []string{"snippet", "authorDetails"})
	var googleApiErr = googleapi.Error{}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			switch response, err := call.Do();  {
			case err == nil:
				for _, message := range response.Items {
					ch <- message
				}

			case errors.As(err, &googleApiErr):
				switch googleApiErr.Code {
				case http.StatusBadRequest, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusUnauthorized:
					return err
				}
			default:
				return err
			}
		}
	}
}

type HandleWatch struct {
	logger slog.Logger
	service *youtube.Service
}

func NewHandleWatch(ctx context.Context) (HandleWatch, error) {
	service, err := youtube.NewService(ctx, option.WithAPIKey(""))  // TODO: Retrive API Key from somewhere
	if err != nil {
		return HandleWatch{}, err
	}
	return HandleWatch{
		logger: *slog.Default(),
		service: service,
	}, nil
}

func (s HandleWatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Retrive broadcast ID from "broadcastId" or "url" query parameters.
	s.logger.Info("Connection requested", "from", r.Host, "uri", r.RequestURI)
	broadcastId := r.FormValue("broadcastId")
	if broadcastId == "" {
		broadcastUrl := r.FormValue("url")

		if after, found := strings.CutPrefix(broadcastUrl, "https://youtube.com/watch?="); found {
			broadcastId = after
		} else {
			s.logger.Info("No broadcastId is specified", "from", r.Host, "uri", r.RequestURI, "ResponseCode", http.StatusBadRequest)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("One of broadcastId or url should be provided."))
			return
		}
	}

	// Accept websocket request
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("Failed to accept websocket request: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "Server is closing by defer statement.")

	// Retrive chatId before doing anything more so that we can reject
	// invalid request
	chatId, err := RetriveLiveChatId(broadcastId, s.service.LiveBroadcasts)
	if err != nil {
		s.logger.Info("[Close] could not retrive chatId", "chatId", broadcastId, "error", err, "ResponseCode", http.StatusBadRequest)
		c.Close(websocket.StatusAbnormalClosure, fmt.Sprintf("Could not retrive chatId for broadcastId %v", broadcastId))
		return
	}

	// Now I confirmed that given broadcastId is valid because I got Live chat ID.
	// Start receiving commands and redirecting messages.

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Receive data from peer
	cmdCh := make(chan Command, 2)
	go CommandReaderGoroutine(ctx, c, cmdCh)

	// Redirect chat messages to peer
	sendCh := make(chan *youtube.LiveChatMessage, 2)
	go ReceiveMessages(ctx, s.service.LiveChatMessages, chatId, sendCh)

LOOP2:
	for {
		select {
		case cmd := <-cmdCh:
			if cmd == COM_BYE {
				cancel()
			}
		case msg := <-sendCh:
			c.Write(ctx, websocket.MessageText, []byte(msg.Snippet.DisplayMessage))
		case <-ctx.Done():
			break LOOP2
		}
	}
	c.Close(websocket.StatusAbnormalClosure, "")
}

func main() {
	ctx := context.Background()
	handler, err := NewHandleWatch(ctx)
	if err != nil {
		log.Fatalf("Could not creaet handler: %v", err)
	}
	handler.logger.Info("Succeed to create handler. Starting server...")

	http.Handle("/watch", handler)
	log.Fatalf("Server killed: %v", http.ListenAndServe(":12539", nil))
}
