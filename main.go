package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/joho/godotenv"
	maxbot "github.com/max-messenger/max-bot-api-client-go"
)

const (
	MaxChances   = 10
	FinalWinners = 5
	FinalReserve = 5
	FinalPrize   = "два билета на Канье Уэста"
)

var api *maxbot.Api
var ctx = context.Background()
var admins []int64

func main() {
	godotenv.Load()
	for _, s := range strings.Split(os.Getenv("ADMIN_IDS"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			admins = append(admins, id)
		}
	}
	openDB("bot.db")

	if len(os.Args) > 2 && os.Args[1] == "load" {
		loadPartners(os.Args[2])
		return
	}

	pool, _ := x509.SystemCertPool()
	pem, err := os.ReadFile("certs/russian_trusted_ca.pem")
	if err != nil {
		log.Fatal(err)
	}
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{
		Timeout:   45 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}

	api, err = maxbot.New(os.Getenv("BOT_TOKEN"), maxbot.WithHTTPClient(client))
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for err := range api.GetErrors() {
			log.Println("max:", err)
		}
	}()

	log.Println("Бот запущен")
	for update := range api.GetUpdates(ctx) {
		handle(update)
	}
}

func loadPartners(file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	var partners []Partner
	if err := json.Unmarshal(data, &partners); err != nil {
		log.Fatal(err)
	}
	for _, p := range partners {
		exec("INSERT OR REPLACE INTO partners VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", p.ID, p.Name, p.RaffleDate,
			p.ChannelID, p.ChannelURL, p.PromoWhere, p.PromoUntil, p.Prize, p.PrizeInstruction, max(p.WinnersCount, 1))
		added := int64(0)
		if codes, err := os.ReadFile(p.PromoFile); err == nil {
			for _, code := range strings.Fields(string(codes)) {
				added += exec("INSERT OR IGNORE INTO promocodes (partner_id, code) VALUES (?, ?)", p.ID, code)
			}
		}
		fmt.Printf("#%d %s (%s): добавлено промокодов %d\n", p.ID, p.Name, p.RaffleDate, added)
	}
}
