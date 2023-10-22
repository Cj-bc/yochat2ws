package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"nhooyr.io/websocket"
)

type Command int

const (
	COM_BYE Command = iota
)

// Retrive Live chat ID for given broadcastId
func RetriveLiveChatId(broadcastId string, service *youtube.VideosService) (string, error) {
	call := service.List([]string{"liveStreamingDetails"})
	call.Id(broadcastId)

	if response, err := call.Do(); err != nil {
		return "", fmt.Errorf("Failed to retrive live chat ID: %w", err)
	} else if len(response.Items) == 0 {
		return "", fmt.Errorf("response.Items for broadcastId %v did not contain anything", broadcastId)
	} else {
		return response.Items[0].LiveStreamingDetails.ActiveLiveChatId, nil
	}
}

// Receive Command from peer and send it to channel
func CommandReaderGoroutine(ctx context.Context, c *websocket.Conn, ch chan<- Command) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if _type, msg, err := c.Read(ctx); err != nil {
				return err
			} else if _type != websocket.MessageText {
				log.Printf("Wrong message type: %v", _type)
			} else if string(msg) == "BYE" {
				ch <- COM_BYE
				return nil
			}
		}
	}
}

func ReceiveMessages(ctx context.Context, service *youtube.LiveChatMessagesService, chatId string, ch chan<- *youtube.LiveChatMessage) error {
	var pageToken = ""
	call := service.List(chatId, []string{"snippet", "authorDetails"})

	apiCallInterval := time.NewTimer(0)
	defer apiCallInterval.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-apiCallInterval.C:
			// For first call, it doesn't know pageToken. So Don't add it
			if pageToken != "" {
				call.PageToken(pageToken)
			}

			if response, err := call.Do(); err != nil {
				return err
			} else {
				pageToken = response.NextPageToken
				for _, message := range response.Items {
					ch <- message
				}

				apiCallInterval.Reset(time.Duration(response.PollingIntervalMillis) * time.Millisecond)
			}
		}
	}
}

type HandleWatch struct {
	logger  slog.Logger
	service *youtube.Service
}

func NewHandleWatch(ctx context.Context) (HandleWatch, error) {
	config, token, err := Authenticate(ctx, "client_secret.json", os.Stdin, os.Stdout)
	if err != nil {
		return HandleWatch{}, fmt.Errorf("Failed to authenticate using client secret: %w", err)
	}
	service, err := youtube.NewService(ctx, option.WithTokenSource(config.TokenSource(ctx, token)))
	if err != nil {
		return HandleWatch{}, err
	}
	return HandleWatch{
		logger:  *slog.Default(),
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
	chatId, err := RetriveLiveChatId(broadcastId, s.service.Videos)
	if err != nil {
		c.Close(websocket.StatusAbnormalClosure, fmt.Sprintf("Could not retrive chatId for broadcastId %v", broadcastId))
		s.logger.Info("[Close] could not retrive chatId", "from", r.Host, "uri", r.RequestURI,
			"broadcastId", broadcastId, "error", err, "ResponseCode", http.StatusBadRequest)
		return
	}

	// Now I confirmed that given broadcastId is valid because I got Live chat ID.
	// Start receiving commands and redirecting messages.

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Receive data from peer
	cmdCh := make(chan Command, 2)
	go func() {
		err := CommandReaderGoroutine(ctx, c, cmdCh)
		close(cmdCh)
		if err != nil {
			s.logger.Info(fmt.Sprintf("Error has occured while reading command from peer: %v", err), "from", r.Host, "uri", r.RequestURI)
		}
	}()

	// Redirect chat messages to peer
	sendCh := make(chan *youtube.LiveChatMessage, 2)
	go func() {
		err := ReceiveMessages(ctx, s.service.LiveChatMessages, chatId, sendCh)
		close(sendCh)
		if err != nil {
			s.logger.Info(fmt.Sprintf("Error has occured while receiving LiveChatMessages: %v", err), "from", r.Host, "uri", r.RequestURI)
		}
	}()

LOOP2:
	for {
		select {
		case cmd, ok := <-cmdCh:
			switch {
			case !ok:
				s.logger.Info("Connection is closed by peer", "from", r.Host, "url", r.RequestURI)
				cancel()
			case cmd == COM_BYE:
				s.logger.Info("Connection closing by BYE command", "from", r.Host, "url", r.RequestURI)
				cancel()
			}
		case msg, ok := <-sendCh:
			if !ok {
				cancel()
				break
			}
			byte, err := msg.MarshalJSON()
			if err != nil {
				s.logger.Info(fmt.Sprintf("Failed to marshal JSON: %v", err), "from", r.Host, "url", r.RequestURI)
				break
			}
			c.Write(ctx, websocket.MessageText, byte)
		case <-ctx.Done():
			break LOOP2
		}
	}
	c.Close(websocket.StatusAbnormalClosure, "")
	s.logger.Info("Connection closed", "from", r.Host, "url", r.RequestURI)
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
