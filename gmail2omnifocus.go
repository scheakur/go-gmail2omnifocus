package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/template"
	"time"

	"code.google.com/p/goauth2/oauth"
	gmail "code.google.com/p/google-api-go-client/gmail/v1"
)

type config struct {
	Address  string `json:"address"`
	ClientId string `json:"clientId"`
	Secret   string `json:"secret"`
}

var (
	authUrl  = "https://accounts.google.com/o/oauth2/auth"
	tokenUrl = "https://accounts.google.com/o/oauth2/token"
	scope    = gmail.MailGoogleComScope

	mailTemplate = template.Must(template.New("task").Parse(`From: {{.From}}
To: {{.To}}
Subject: {{.Subject}}
Content-Type: text/plain; charset=UTF-8

{{.Body}}
`))
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gmail2omnifocus task\r\n")
		os.Exit(2)
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	cacheFile := os.ExpandEnv("$HOME/.config/gmail2omnifocus/cache.json")
	gm, err := newGmailer(cfg.ClientId, cfg.Secret, cacheFile)
	if err != nil {
		log.Fatal(err)
	}

	task := flag.Arg(0)
	//TODO use body?
	err = gm.send(cfg.Address, task, "")
	if err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (*config, error) {
	f, err := os.Open(os.ExpandEnv("$HOME/.config/gmail2omnifocus/config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg config
	err = json.NewDecoder(f).Decode(&cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

type gmailer struct {
	service *gmail.Service
}

type message struct {
	From    string
	To      string
	Subject string
	Body    string
}

func newGmailer(clientId, secret, cacheFile string) (*gmailer, error) {
	config := &oauth.Config{
		ClientId:     clientId,
		ClientSecret: secret,
		Scope:        scope,
		AuthURL:      authUrl,
		TokenURL:     tokenUrl,
		TokenCache:   oauth.CacheFile(cacheFile),
	}

	client, err := auth(config)
	if err != nil {
		return nil, err
	}
	svc, err := gmail.New(client)
	if err != nil {
		return nil, err
	}
	return &gmailer{
		service: svc,
	}, nil
}

func (g *gmailer) send(to, subject, body string) error {
	from := "me"

	mail := new(bytes.Buffer)

	mailTemplate.Execute(mail, message{
		From:    from,
		To:      to,
		Subject: encodeRFC2047(subject),
		Body:    encodeRFC2047(body),
	})

	msg := gmail.Message{}
	msg.Raw = base64.URLEncoding.EncodeToString(mail.Bytes())

	_, err := g.service.Users.Messages.Send(from, &msg).Do()

	return err
}

func encodeRFC2047(str string) string {
	a := mail.Address{str, ""}
	return strings.Trim(a.String(), " <>")
}

func auth(config *oauth.Config) (*http.Client, error) {
	transport := &oauth.Transport{
		Config:    config,
		Transport: http.DefaultTransport,
	}
	if _, err := config.TokenCache.Token(); err != nil {
		code := authWeb(config)
		if _, err := transport.Exchange(code); err != nil {
			return nil, err
		}
	}
	return transport.Client(), nil
}

func authWeb(config *oauth.Config) string {
	ch := make(chan string)
	randState := fmt.Sprintf("st%d", time.Now().UnixNano())
	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/favicon.ico" {
			http.Error(rw, "", 404)
			return
		}
		if req.FormValue("state") != randState {
			log.Printf("State doesn't match: req = %#v", req)
			http.Error(rw, "", 500)
			return
		}
		if code := req.FormValue("code"); code != "" {
			fmt.Fprintf(rw, "<h1>Success</h1>Authorized.")
			rw.(http.Flusher).Flush()
			ch <- code
			return
		}
		log.Printf("no code")
		http.Error(rw, "", 500)
	}))
	defer ts.Close()

	config.RedirectURL = ts.URL
	authUrl := config.AuthCodeURL(randState)
	go open(authUrl)
	log.Printf("Authorize this app at: %s", authUrl)
	code := <-ch
	return code
}

func open(url string) {
	commands := map[string]string{
		"darwin": "open",
		"linux":  "xdg-open",
	}
	if cmd, ok := commands[runtime.GOOS]; ok {
		exec.Command(cmd, url).Run()
	}
}
