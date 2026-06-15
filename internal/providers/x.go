package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type XClient struct {
	AccessToken  string
	ClientID     string
	ClientSecret string
	APIBaseURL   string
	HTTP         *http.Client
}

type XBookmark struct {
	ID            string         `json:"id"`
	Text          string         `json:"text"`
	AuthorID      string         `json:"author_id"`
	Entities      map[string]any `json:"entities"`
	PublicMetrics map[string]any `json:"public_metrics"`
}

type XUser struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Name            string `json:"name"`
	ProfileImageURL string `json:"profile_image_url"`
}

type XToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

type XBookmarkPage struct {
	Bookmarks []XBookmark
	Users     map[string]XUser
	NextToken string
}

func (c XClient) ExchangeCode(ctx context.Context, code, redirectURI, verifier string) (XToken, error) {
	if c.ClientID == "" {
		return XToken{}, ErrNotConfigured
	}
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", redirectURI)
	values.Set("code_verifier", verifier)
	values.Set("client_id", c.ClientID)
	var decoded struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := c.form(ctx, http.MethodPost, "/2/oauth2/token", values, "", &decoded); err != nil {
		return XToken{}, err
	}
	if decoded.AccessToken == "" {
		return XToken{}, fmt.Errorf("x token response missing access_token")
	}
	return XToken(decoded), nil
}

func (c XClient) Refresh(ctx context.Context, refreshToken string) (XToken, error) {
	if c.ClientID == "" || refreshToken == "" {
		return XToken{}, ErrNotConfigured
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", c.ClientID)
	var decoded struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := c.form(ctx, http.MethodPost, "/2/oauth2/token", values, "", &decoded); err != nil {
		return XToken{}, err
	}
	if decoded.AccessToken == "" {
		return XToken{}, fmt.Errorf("x refresh response missing access_token")
	}
	return XToken(decoded), nil
}

func (c XClient) Revoke(ctx context.Context, token string) error {
	if c.ClientID == "" || token == "" {
		return ErrNotConfigured
	}
	values := url.Values{}
	values.Set("token", token)
	values.Set("client_id", c.ClientID)
	return c.form(ctx, http.MethodPost, "/2/oauth2/revoke", values, "", nil)
}

func (c XClient) Profile(ctx context.Context, accessToken string) (XUser, error) {
	if accessToken == "" {
		return XUser{}, ErrNotConfigured
	}
	values := url.Values{}
	values.Set("user.fields", "profile_image_url,name,username")
	endpoint := c.apiBase() + "/2/users/me?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return XUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var decoded struct {
		Data XUser `json:"data"`
	}
	if err := c.do(req, &decoded); err != nil {
		return XUser{}, err
	}
	if decoded.Data.ID == "" {
		return XUser{}, fmt.Errorf("x profile response missing user id")
	}
	return decoded.Data, nil
}

func (c XClient) Bookmarks(ctx context.Context, userID string, paginationToken string, max int) ([]XBookmark, string, error) {
	page, err := c.BookmarkPage(ctx, userID, paginationToken, max)
	if err != nil {
		return nil, "", err
	}
	return page.Bookmarks, page.NextToken, nil
}

func (c XClient) BookmarkPage(ctx context.Context, userID string, paginationToken string, max int) (XBookmarkPage, error) {
	if c.AccessToken == "" {
		return XBookmarkPage{}, ErrNotConfigured
	}
	if max <= 0 || max > 100 {
		max = 100
	}
	values := url.Values{}
	values.Set("max_results", fmt.Sprintf("%d", max))
	values.Set("tweet.fields", "author_id,created_at,entities,public_metrics")
	values.Set("expansions", "author_id")
	values.Set("user.fields", "username,name,profile_image_url")
	if paginationToken != "" {
		values.Set("pagination_token", paginationToken)
	}
	endpoint := c.apiBase() + "/2/users/" + url.PathEscape(userID) + "/bookmarks?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return XBookmarkPage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	var decoded struct {
		Data     []XBookmark `json:"data"`
		Includes struct {
			Users []XUser `json:"users"`
		} `json:"includes"`
		Meta struct {
			NextToken string `json:"next_token"`
		} `json:"meta"`
	}
	if err := c.do(req, &decoded); err != nil {
		return XBookmarkPage{}, err
	}
	users := map[string]XUser{}
	for _, user := range decoded.Includes.Users {
		users[user.ID] = user
	}
	return XBookmarkPage{Bookmarks: decoded.Data, Users: users, NextToken: decoded.Meta.NextToken}, nil
}

func (c XClient) form(ctx context.Context, method, endpoint string, values url.Values, bearer string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase()+endpoint, bytes.NewBufferString(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if c.ClientSecret != "" {
		credential := base64.StdEncoding.EncodeToString([]byte(c.ClientID + ":" + c.ClientSecret))
		req.Header.Set("Authorization", "Basic "+credential)
	}
	return c.do(req, dst)
}

func (c XClient) do(req *http.Request, dst any) error {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("x status %d", resp.StatusCode)
	}
	if dst == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func (c XClient) apiBase() string {
	if c.APIBaseURL == "" {
		return "https://api.twitter.com"
	}
	return strings.TrimRight(c.APIBaseURL, "/")
}
