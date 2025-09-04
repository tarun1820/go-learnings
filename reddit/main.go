package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds configuration for Reddit and email
type Config struct {
	RedditClientID     string
	RedditClientSecret string
	RedditUsername     string
	RedditPassword     string

	SMTPHost  string
	SMTPPort  string
	EmailFrom string
	EmailTo   string
	EmailUser string
	EmailPass string

	Subreddit string
	Keywords  []string
}

// RedditAuthResponse structure for OAuth token response
type RedditAuthResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// RedditPost structure for relevant part of Reddit posts response
type RedditPost struct {
	Data struct {
		Children []struct {
			Data struct {
				Title      string  `json:"title"`
				CreatedUTC float64 `json:"created_utc"`
				Permalink  string  `json:"permalink"`
				URL        string  `json:"url"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	return &Config{
		RedditClientID:     os.Getenv("REDDIT_CLIENT_ID"),
		RedditClientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
		RedditUsername:     os.Getenv("REDDIT_USERNAME"),
		RedditPassword:     os.Getenv("REDDIT_PASSWORD"),

		SMTPHost:  os.Getenv("SMTP_HOST"),
		SMTPPort:  os.Getenv("SMTP_PORT"),
		EmailFrom: os.Getenv("EMAIL_FROM"),
		EmailTo:   os.Getenv("EMAIL_TO"),
		EmailUser: os.Getenv("EMAIL_USER"),
		EmailPass: os.Getenv("EMAIL_PASS"),

		Subreddit: "HyderabadBuySell",
		Keywords:  []string{"table", "chair"},
	}
}

// GetRedditAccessToken authenticates with Reddit and returns access token
func GetRedditAccessToken(cfg *Config) (string, error) {
	req, err := http.NewRequest("POST", "https://www.reddit.com/api/v1/access_token", strings.NewReader("grant_type=password&username="+cfg.RedditUsername+"&password="+cfg.RedditPassword))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(cfg.RedditClientID, cfg.RedditClientSecret)
	req.Header.Set("User-Agent", "Go:HyderabadBuySellMonitor:v1.0 (by /u/"+cfg.RedditUsername+")")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to get token: %v", resp.Status)
	}

	var tokenResp RedditAuthResponse
	err = json.NewDecoder(resp.Body).Decode(&tokenResp)
	if err != nil {
		return "", err
	}
	return tokenResp.AccessToken, nil
}

// fetchAndFilterTodayPosts fetches posts from subreddit created today with keywords
func fetchAndFilterTodayPosts(cfg *Config, token string) ([]struct {
	Title     string
	Permalink string
	Created   time.Time
}, error) {
	url := fmt.Sprintf("https://oauth.reddit.com/r/%s/new?limit=100", cfg.Subreddit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Go:HyderabadBuySellMonitor:v1.0 (by /u/"+cfg.RedditUsername+")")
	req.Header.Set("Authorization", "bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var posts RedditPost
	err = json.NewDecoder(resp.Body).Decode(&posts)
	if err != nil {
		return nil, err
	}

	today := time.Now().UTC()
	startOfDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).Unix()
	endOfDay := time.Date(today.Year(), today.Month(), today.Day(), 23, 59, 59, 0, time.UTC).Unix()

	matching := []struct {
		Title     string
		Permalink string
		Created   time.Time
	}{}

	for _, child := range posts.Data.Children {
		postTime := int64(child.Data.CreatedUTC)
		if postTime >= startOfDay && postTime <= endOfDay {
			titleLower := strings.ToLower(child.Data.Title)
			for _, kw := range cfg.Keywords {
				if strings.Contains(titleLower, kw) {
					matching = append(matching, struct {
						Title     string
						Permalink string
						Created   time.Time
					}{
						Title:     child.Data.Title,
						Permalink: "https://reddit.com" + child.Data.Permalink,
						Created:   time.Unix(postTime, 0).UTC(),
					})
					break
				}
			}
		}
	}

	return matching, nil
}

// ComposeEmailBody prepares the email content string
func ComposeEmailBody(posts []struct {
	Title     string
	Permalink string
	Created   time.Time
}) string {
	var buffer bytes.Buffer
	buffer.WriteString("Posts for today with keywords (table, chair) from r/HyderabadBuySell:\n\n")
	for _, post := range posts {
		buffer.WriteString(fmt.Sprintf("- %s\n  %s\n  Posted at (UTC): %s\n\n", post.Title, post.Permalink, post.Created.Format(time.RFC1123)))
	}
	return buffer.String()
}

// SendEmail sends email using SMTP server
func SendEmail(cfg *Config, subject, body string) error {
	auth := smtp.PlainAuth("", cfg.EmailUser, cfg.EmailPass, cfg.SMTPHost)

	header := make(map[string]string)
	header["From"] = cfg.EmailFrom
	header["To"] = cfg.EmailTo
	header["Subject"] = subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = "text/plain; charset=\"utf-8\""

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + body

	// Connect to SMTP with TLS
	serverAddr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         cfg.SMTPHost,
	}

	conn, err := tls.Dial("tcp", serverAddr, tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(cfg.EmailFrom); err != nil {
		return err
	}

	if err = c.Rcpt(cfg.EmailTo); err != nil {
		return err
	}

	w, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	return c.Quit()
}

func main() {
	// Load .env file from project folder
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	cfg := LoadConfig()
	// Validate required config
	if cfg.RedditClientID == "" || cfg.RedditClientSecret == "" || cfg.RedditUsername == "" || cfg.RedditPassword == "" {
		log.Fatal("Missing Reddit API credentials in environment variables")
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort == "" || cfg.EmailFrom == "" || cfg.EmailTo == "" || cfg.EmailUser == "" || cfg.EmailPass == "" {
		log.Fatal("Missing SMTP/email configuration in environment variables")
	}

	token, err := GetRedditAccessToken(cfg)
	if err != nil {
		log.Fatalf("Failed to authenticate Reddit: %v", err)
	}
	log.Println("Got Reddit access token")

	posts, err := fetchAndFilterTodayPosts(cfg, token)
	if err != nil {
		log.Fatalf("Failed to fetch or filter posts: %v", err)
	}

	if len(posts) == 0 {
		log.Println("No matching posts found today")
		return
	}

	emailBody := ComposeEmailBody(posts)
	log.Println("Sending email notification...")
	err = SendEmail(cfg, "HyderabadBuySell Alert: Table/Chair Posts Today", emailBody)
	if err != nil {
		log.Fatalf("Failed to send email: %v", err)
	}

	log.Println("Email sent successfully")
}
