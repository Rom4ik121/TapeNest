// Command initdata-mock generates valid, signed Telegram Mini App initData for
// local development and tests (spec §11). The bot token is read from the
// environment (TELEGRAM_BOT_TOKEN by default) and is never printed.
//
//	TELEGRAM_BOT_TOKEN=... go run . -user-id 42 -first-name Roma -lang ru
//	go run . -format hash -theme dark   # URL fragment for opening the app in a browser
//	go run . -format json               # {"initData": "..."} body for POST /api/v1/auth/telegram
//	go run . -validate "<raw initData>" # verify signature + 24h TTL
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/tapenest/tapenest/tools/initdata-mock/initdata"
)

var themes = map[string]map[string]string{
	"dark": {
		"bg_color": "#16141c", "secondary_bg_color": "#221d2b", "text_color": "#e7e4de",
		"hint_color": "#a89fb5", "link_color": "#eeaa11", "button_color": "#eeaa11",
		"button_text_color": "#16141c", "header_bg_color": "#16141c",
	},
	"light": {
		"bg_color": "#e7e4de", "secondary_bg_color": "#f4f2ee", "text_color": "#16141c",
		"hint_color": "#585062", "link_color": "#962768", "button_color": "#bb3381",
		"button_text_color": "#ffffff", "header_bg_color": "#e7e4de",
	},
}

func main() {
	var (
		tokenEnv   = flag.String("token-env", "TELEGRAM_BOT_TOKEN", "env var holding the bot token")
		userID     = flag.Int64("user-id", 100000001, "Telegram user id")
		firstName  = flag.String("first-name", "Dev", "first name")
		lastName   = flag.String("last-name", "", "last name")
		username   = flag.String("username", "dev_user", "username")
		lang       = flag.String("lang", "ru", "language_code")
		ageSec     = flag.Int64("age", 0, "auth_date = now - age seconds (use >86400 to test expiry)")
		queryID    = flag.String("query-id", "", "query_id")
		startParam = flag.String("start-param", "", "start_param")
		format     = flag.String("format", "raw", "raw | json | hash")
		theme      = flag.String("theme", "dark", "dark | light (for -format hash)")
		validate   = flag.String("validate", "", "validate the given raw initData instead of generating")
		signature  = flag.String("signature", "mock-signature-not-verifiable-locally", "placeholder Ed25519 signature field (empty to omit)")
	)
	flag.Parse()

	token := os.Getenv(*tokenEnv)
	if token == "" {
		fmt.Fprintf(os.Stderr, "initdata-mock: env %s is empty\n", *tokenEnv)
		os.Exit(2)
	}

	if *validate != "" {
		v, err := initdata.Validate(token, *validate, 24*time.Hour, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "INVALID:", err)
			os.Exit(1)
		}
		fmt.Println("VALID user=" + v.Get("user"))
		return
	}

	raw, err := initdata.Sign(token, initdata.Params{
		User: initdata.User{
			ID: *userID, FirstName: *firstName, LastName: *lastName,
			Username: *username, LanguageCode: *lang, AllowsWrite: true,
		},
		AuthDate:   time.Now().Add(-time.Duration(*ageSec) * time.Second),
		QueryID:    *queryID,
		StartParam: *startParam,
		Signature:  *signature,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "initdata-mock:", err)
		os.Exit(1)
	}

	switch *format {
	case "raw":
		fmt.Println(raw)
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"initData": raw})
	case "hash":
		tp, ok := themes[*theme]
		if !ok {
			fmt.Fprintln(os.Stderr, "initdata-mock: unknown theme", *theme)
			os.Exit(2)
		}
		tpJSON, _ := json.Marshal(tp)
		lp := url.Values{}
		lp.Set("tgWebAppData", raw)
		lp.Set("tgWebAppVersion", "8.0")
		lp.Set("tgWebAppPlatform", "tdesktop")
		lp.Set("tgWebAppThemeParams", string(tpJSON))
		fmt.Println("#" + lp.Encode())
	default:
		fmt.Fprintln(os.Stderr, "initdata-mock: unknown -format", *format)
		os.Exit(2)
	}
}
