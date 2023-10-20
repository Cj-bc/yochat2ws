package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)
func Authenticate(ctx context.Context, secretFile string) (*oauth2.Config, *oauth2.Token, error) {
	// Do OAuth
	d, err := os.ReadFile(secretFile)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to read client_secret.json file: %w", err)
	}
	config, err := google.ConfigFromJSON(d, youtube.YoutubeReadonlyScope)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to read config from JSON: %w", err)
	}

	const state = "TestStateCode" // TODO: DO NOT USE HARDCODED CODE HERE
	url := config.AuthCodeURL(state, oauth2.AccessTypeOffline)
	fmt.Printf("Access URL below and approve. Then paste token below:\n%s\n", url)

	var code string
	if i, err := fmt.Scan(&code); err != nil || i < 1 {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to receive token: %w", err)
	}

	token, err := config.Exchange(ctx, code)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to exchange &OAuth2 token: %w", err)
	}

	return config, token, nil
}
