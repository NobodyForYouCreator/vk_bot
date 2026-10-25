package main

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB
var moscow, _ = time.LoadLocation("Europe/Moscow")

const schema = `
CREATE TABLE IF NOT EXISTS users (user_id INTEGER PRIMARY KEY, name TEXT, created_at TEXT, waiting_name INTEGER DEFAULT 0);
CREATE TABLE IF NOT EXISTS partners (id INTEGER PRIMARY KEY, name TEXT, raffle_date TEXT UNIQUE, channel_id INTEGER,
    channel_url TEXT, promo_where TEXT, promo_until TEXT, prize TEXT, prize_instruction TEXT, winners_count INTEGER);
CREATE TABLE IF NOT EXISTS promocodes (partner_id INTEGER, code TEXT, user_id INTEGER,
    UNIQUE (partner_id, code), UNIQUE (partner_id, user_id));
CREATE TABLE IF NOT EXISTS promo_requests (partner_id INTEGER, user_id INTEGER);
CREATE TABLE IF NOT EXISTS entries (partner_id INTEGER, user_id INTEGER, created_at TEXT, UNIQUE (partner_id, user_id));
CREATE TABLE IF NOT EXISTS chances (user_id INTEGER, day TEXT, partner_id INTEGER, UNIQUE (user_id, day));
CREATE TABLE IF NOT EXISTS winners (id INTEGER PRIMARY KEY AUTOINCREMENT, partner_id INTEGER, user_id INTEGER,
    reserve INTEGER, status TEXT, full_name TEXT DEFAULT '', created_at TEXT);
`

type Partner struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	RaffleDate       string `json:"raffle_date"`
	ChannelID        int64  `json:"channel_id"`
	ChannelURL       string `json:"channel_url"`
	PromoWhere       string `json:"promo_where"`
	PromoUntil       string `json:"promo_until"`
	Prize            string `json:"prize"`
	PrizeInstruction string `json:"prize_instruction"`
	WinnersCount     int    `json:"winners_count"`
	PromoFile        string `json:"promo_file"`
}

func openDB(path string) {
	var err error
	if db, err = sql.Open("sqlite", path); err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		log.Fatal(err)
	}
}

func now() string {
	return time.Now().In(moscow).Format("2006-01-02 15:04:05")
}

func today() string {
	return now()[:10]
}

func exec(query string, args ...any) int64 {
	res, err := db.Exec(query, args...)
	if err != nil {
		log.Println("db:", err)
		return -1
	}
	n, _ := res.RowsAffected()
	return n
}

func num(query string, args ...any) int {
	var n int
	db.QueryRow(query, args...).Scan(&n)
	return n
}

func str(query string, args ...any) string {
	var s string
	db.QueryRow(query, args...).Scan(&s)
	return s
}

func ids(query string, args ...any) []int64 {
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Println("db:", err)
		return nil
	}
	defer rows.Close()
	var result []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		result = append(result, id)
	}
	return result
}

func getPartner(where string, args ...any) *Partner {
	p := &Partner{}
	err := db.QueryRow(`SELECT id, name, raffle_date, channel_id, channel_url, promo_where, promo_until, prize,
		prize_instruction, winners_count FROM partners `+where, args...).Scan(&p.ID, &p.Name, &p.RaffleDate,
		&p.ChannelID, &p.ChannelURL, &p.PromoWhere, &p.PromoUntil, &p.Prize, &p.PrizeInstruction, &p.WinnersCount)
	if err != nil {
		return nil
	}
	return p
}

func execTx(queries ...[]any) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, q := range queries {
		if q == nil {
			continue
		}
		if _, err := tx.Exec(q[0].(string), q[1:]...); err != nil {
			tx.Rollback()
			log.Println("db:", err)
			return err
		}
	}
	return tx.Commit()
}

func chanceQuery(userID, partnerID int64) []any {
	if chances(userID) >= MaxChances || num("SELECT COUNT(*) FROM winners WHERE partner_id = 0") > 0 {
		return nil
	}
	return []any{"INSERT OR IGNORE INTO chances VALUES (?, ?, ?)", userID, today(), partnerID}
}

func chances(userID int64) int {
	return num("SELECT COUNT(*) FROM chances WHERE user_id = ?", userID)
}
