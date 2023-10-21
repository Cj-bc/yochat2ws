package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

// Returns path to token cache file.
// If directory does not exist, make it.
func TokenCacheFilePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = ""
	}
	return path.Join(cacheDir, "yochat2ws", "credential.json")
}

// Get oauth2.Token. If cache file exists, read from there.
// If not, do oauth2 exchange.
func GetToken(ctx context.Context, config *oauth2.Config, input io.Reader, output io.Writer) (*oauth2.Token, error) {
	if tok, err := GetCachedToken(); err != nil {
		const state = "TestStateCode" // TODO: DO NOT USE HARDCODED CODE HERE
		url := config.AuthCodeURL(state, oauth2.AccessTypeOffline)
		fmt.Fprintf(output, "Access URL below and approve. Then paste token below:\n%s\n", url)

		var code string
		if i, err := fmt.Fscan(input, &code); err != nil || i < 1 {
			return &oauth2.Token{}, fmt.Errorf("Failed to receive token: %w", err)
		}

		token, err := config.Exchange(ctx, code)
		if err != nil {
			return &oauth2.Token{}, fmt.Errorf("Failed to exchange OAuth2 token: %w", err)
		}

		return token, nil
	} else {
		return &tok, nil
	}
}

// Try to get cached oauth2.Token and return it.
// If cached file is not exist, returns error.
func GetCachedToken() (oauth2.Token, error) {
	f, err := os.Open(TokenCacheFilePath())
	defer f.Close()
	if err != nil {
		return oauth2.Token{}, err
	} else {
		t := oauth2.Token{}
		err = json.NewDecoder(f).Decode(&t)
		return t, err
	}
}

// Do google OAuth2 authentication and return its oauth2.Config and oauth2.Token
func Authenticate(ctx context.Context, secretFile string, input io.Reader, output io.Writer) (*oauth2.Config, *oauth2.Token, error) {
	// Do OAuth
	d, err := os.ReadFile(secretFile)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to read client_secret.json file: %w", err)
	}
	config, err := google.ConfigFromJSON(d, youtube.YoutubeReadonlyScope)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to read config from JSON: %w", err)
	}

	token, err := GetToken(ctx, config, input, output)
	if err != nil {
		return &oauth2.Config{}, &oauth2.Token{}, fmt.Errorf("Failed to get token: %w", err)
	}

	return config, token, nil
}
