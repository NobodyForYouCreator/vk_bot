package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

var (
	mu       sync.Mutex
	messages = map[string][]string{}
	members  = map[int64]bool{}
	blocked  = map[string]bool{}
)

func fakeMax(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var m struct {
		Text         string `json:"text"`
		Notification string `json:"notification"`
		Message      *struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	json.Unmarshal(body, &m)
	mu.Lock()
	defer mu.Unlock()
	switch {
	case r.URL.Path == "/messages":
		to := r.URL.Query().Get("user_id")
		if blocked[to] {
			w.WriteHeader(403)
			w.Write([]byte(`{"code":"chat.denied","message":"blocked"}`))
			return
		}
		messages[to] = append(messages[to], m.Text)
		w.Write([]byte(`{"message":{"recipient":{"chat_type":"dialog"},"body":{"mid":"1","seq":1},"timestamp":1}}`))
	case r.URL.Path == "/answers":
		text := "POPUP: " + m.Notification
		if m.Message != nil {
			text = m.Message.Text
		}
		messages["cb"] = append(messages["cb"], text)
		w.Write([]byte(`{"success":true}`))
	case strings.HasSuffix(r.URL.Path, "/members"):
		id, _ := strconv.ParseInt(r.URL.Query().Get("user_ids"), 10, 64)
		if members[id] {
			fmt.Fprintf(w, `{"members":[{"user_id":%d}]}`, id)
		} else {
			w.Write([]byte(`{"members":[]}`))
		}
	}
}

func lastTo(to string) string {
	mu.Lock()
	defer mu.Unlock()
	if len(messages[to]) == 0 {
		return ""
	}
	return messages[to][len(messages[to])-1]
}

func user(id int64) schemes.User { return schemes.User{UserId: id, Name: fmt.Sprintf("U%d", id)} }

func write(id int64, text string) {
	handle(&schemes.MessageCreatedUpdate{Message: schemes.Message{Sender: user(id),
		Recipient: schemes.Recipient{ChatType: schemes.DIALOG}, Body: schemes.MessageBody{Text: text}}})
}

func press(id int64, payload string) string {
	handle(&schemes.MessageCallbackUpdate{Callback: schemes.Callback{CallbackID: "cb", Payload: payload, User: user(id)}})
	return lastTo("cb")
}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("ожидали %q, получили %q", want, got)
	}
}

func setup(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(fakeMax))
	t.Cleanup(srv.Close)
	api, _ = maxbot.New("token", maxbot.WithBaseURL(srv.URL+"/"))
	admins = []int64{777}
	messages, members, blocked = map[string][]string{}, map[int64]bool{}, map[string]bool{}
	openDB(":memory:")
	exec("INSERT INTO partners VALUES (1, 'P1', ?, -100, 'https://max.ru/p1', 'сайте', '01.12', 'мерч', 'напиши нам.', 2)", today())
	exec("INSERT INTO promocodes (partner_id, code) VALUES (1, 'CODE-1')")
	for id := int64(1); id <= 12; id++ {
		saveUser(user(id))
		members[id] = true
	}
}

func TestUserFlow(t *testing.T) {
	setup(t)
	expect(t, press(1, "promo"), "CODE-1")
	expect(t, press(1, "promo"), "CODE-1")
	expect(t, press(2, "promo"), "закончились")
	expect(t, press(1, "check:99"), "уже завершен")

	members[1] = false
	expect(t, press(1, "check:1"), "Пока не вижу подписку")
	members[1] = true
	expect(t, press(1, "check:1"), "1 из 10")
	expect(t, press(1, "check:1"), "уже участвуешь")
	expect(t, press(2, "check:1"), "1 из 10")

	write(777, "/draw 1")
	expect(t, press(3, "check:1"), "уже проведен")
	expect(t, press(3, "raffle"), "уже проведен")
}

func TestAdminArgs(t *testing.T) {
	setup(t)
	press(1, "check:1")
	for _, cmd := range []string{"/draw", "/draw abc", "/confirm abc", "/give", "/send"} {
		write(777, cmd)
		expect(t, lastTo("777"), "Не хватает аргумента")
	}
	if num("SELECT COUNT(*) FROM winners") != 0 {
		t.Error("неверная команда провела розыгрыш")
	}
	write(777, "/give 12345")
	expect(t, lastTo("777"), "нельзя заменить")
}

func TestFinalRetryAndGive(t *testing.T) {
	setup(t)
	for id := int64(1); id <= 12; id++ {
		exec("INSERT INTO chances VALUES (?, '2026-01-01', 1)", id)
	}
	write(777, "/final")
	expect(t, lastTo("777"), "Разослать уведомления: /confirm 0")
	execTx(chanceQuery(1, 1))
	if chances(1) != 1 {
		t.Error("шанс начислен после финального розыгрыша")
	}

	first := num("SELECT id FROM winners WHERE partner_id = 0 AND reserve = 0 ORDER BY id LIMIT 1")
	firstUser := strconv.Itoa(num("SELECT user_id FROM winners WHERE id = ?", first))
	blocked[firstUser] = true
	write(777, "/confirm 0")
	expect(t, lastTo("777"), "Уведомлено: 4")
	blocked[firstUser] = false
	write(777, "/confirm 0")
	expect(t, lastTo("777"), "Уведомлено: 1")

	userID := int64(num("SELECT user_id FROM winners WHERE id = ?", first))
	press(userID, fmt.Sprintf("claim:%d", first))
	write(777, fmt.Sprintf("/give %d", first))
	expect(t, lastTo("777"), "Уведомлено: 1")
	write(777, fmt.Sprintf("/give %d", first))
	expect(t, lastTo("777"), "нельзя заменить")
	if n := num("SELECT COUNT(*) FROM winners WHERE partner_id = 0 AND reserve = 0 AND status = 'sent'"); n != FinalWinners {
		t.Errorf("активных победителей %d, а должно быть %d", n, FinalWinners)
	}
	expect(t, press(userID, fmt.Sprintf("claim:%d", first)), "недоступен")
	write(userID, "Иван Петров")
	expect(t, lastTo(firstUser), "больше недоступен")

	second := num("SELECT id FROM winners WHERE partner_id = 0 AND status = 'sent' ORDER BY id LIMIT 1")
	secondUser := int64(num("SELECT user_id FROM winners WHERE id = ?", second))
	press(secondUser, fmt.Sprintf("claim:%d", second))
	write(secondUser, "Иван Петров")
	expect(t, lastTo(strconv.FormatInt(secondUser, 10)), "Данные получены")
}

func TestBroadcastAndCSV(t *testing.T) {
	setup(t)
	blocked["5"] = true
	write(777, "/send привет")
	time.Sleep(time.Second)
	expect(t, lastTo("777"), "доставлено 12 из 13")

	if writeCSV("/nonexistent/dir/x.csv", nil, nil) == nil {
		t.Error("ошибка записи CSV не обнаружена")
	}
}

func fail(t *testing.T, when string) {
	t.Helper()
	if _, err := db.Exec("CREATE TRIGGER fail " + when + " BEGIN SELECT RAISE(ABORT, 'disk full'); END"); err != nil {
		t.Fatal(err)
	}
}

func repair() { db.Exec("DROP TRIGGER fail") }

func TestRegistrationIsAtomic(t *testing.T) {
	setup(t)
	fail(t, "BEFORE INSERT ON chances")
	expect(t, press(1, "check:1"), "POPUP: "+oops)
	if num("SELECT COUNT(*) FROM entries") != 0 {
		t.Error("заявка сохранилась без шанса")
	}
	repair()
	expect(t, press(1, "check:1"), "1 из 10")
}

func TestConfirmAndGiveAreAtomic(t *testing.T) {
	setup(t)
	for id := int64(1); id <= 12; id++ {
		exec("INSERT INTO chances VALUES (?, '2026-01-01', 1)", id)
	}
	write(777, "/final")

	fail(t, "BEFORE UPDATE OF status ON winners WHEN NEW.status = 'sent'")
	write(777, "/confirm 0")
	expect(t, lastTo("777"), "Уведомлено: 0, не удалось: 5")
	for _, u := range ids("SELECT user_id FROM winners") {
		if lastTo(strconv.FormatInt(u, 10)) != "" {
			t.Error("сообщение ушло, хотя статус не сохранился")
		}
	}
	repair()
	write(777, "/confirm 0")
	expect(t, lastTo("777"), "Уведомлено: 5, не удалось: 0")

	first := num("SELECT id FROM winners WHERE partner_id = 0 AND reserve = 0 ORDER BY id LIMIT 1")
	userID := int64(num("SELECT user_id FROM winners WHERE id = ?", first))
	expect(t, press(userID, fmt.Sprintf("claim:%d", first)), "имя и фамилию")

	fail(t, "BEFORE UPDATE OF reserve ON winners WHEN NEW.reserve = 0")
	write(777, fmt.Sprintf("/give %d", first))
	expect(t, lastTo("777"), oops)
	if str("SELECT status FROM winners WHERE id = ?", first) != "sent" {
		t.Error("старый победитель заменен, а резервный не назначен")
	}
	repair()
	write(777, fmt.Sprintf("/give %d", first))
	expect(t, lastTo("777"), "Уведомлено: 1")
	if n := num("SELECT COUNT(*) FROM winners WHERE reserve = 0 AND status = 'sent'"); n != FinalWinners {
		t.Errorf("активных победителей %d, а должно быть %d", n, FinalWinners)
	}
}

func TestDrawNotSavedWithoutCSV(t *testing.T) {
	setup(t)
	press(1, "check:1")
	dir, _ := os.Getwd()
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	write(777, "/draw 1")
	expect(t, lastTo("777"), "Не удалось сохранить выгрузку")
	if num("SELECT COUNT(*) FROM winners") != 0 {
		t.Error("победители сохранены без выгрузки")
	}
}
