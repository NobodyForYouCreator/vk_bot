package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

const welcome = "<b>Добро пожаловать в Бот розыгрышей</b> 🎪"
const oops = "Что-то пошло не так, попробуй еще раз чуть позже"

var howTo = "<b>Как участвовать</b>\n\n" +
	"1. Каждый день в боте новый партнер.\n" +
	"2. Нажми «Получить промокод» — получишь бонус от партнера дня.\n" +
	"3. Нажми «Участвовать в розыгрыше», подпишись на партнера и нажми «Проверить подписку».\n" +
	"4. Каждый активный день = +1 шанс в финальном розыгрыше (максимум 10)."

var rules = "<b>Правила акции</b>\n\n" +
	"• Один промокод и одна заявка в розыгрыше каждого партнера.\n" +
	"• Для участия в розыгрыше нужна подписка на партнера.\n" +
	"• Один активный день = один шанс в финальном розыгрыше, максимум 10.\n" +
	"• Победители выбираются случайно и подтверждаются организатором."

var btnMenu = maxbot.Row(maxbot.Btn("« В меню", "menu"))
var btnRaffle = maxbot.Row(maxbot.Btn("🎟 Участвовать в розыгрыше", "raffle"))

func menuKb() *maxbot.Keyboard {
	return maxbot.InlineKeyboard(
		maxbot.Row(maxbot.Btn("🎁 Получить промокод", "promo")),
		btnRaffle,
		maxbot.Row(maxbot.Btn("❓ Как участвовать", "how")),
		maxbot.Row(maxbot.Btn("⭐ Мои шансы", "chances")),
		maxbot.Row(maxbot.Btn("📜 Правила акции", "rules")),
	)
}

func send(userID int64, text string, kb *maxbot.Keyboard) error {
	m := maxbot.NewMessage().SetUser(userID).SetText(text).SetFormat(schemes.HTML)
	if kb != nil {
		m.AddKeyboard(kb)
	}
	err := api.Messages.Send(ctx, m)
	if err != nil {
		log.Println("send:", err)
	}
	return err
}

func reply(callbackID, text string, kb *maxbot.Keyboard) {
	body := &schemes.NewMessageBody{Text: text, Format: schemes.HTML, Attachments: []any{}}
	if kb != nil {
		body.Attachments = append(body.Attachments, schemes.NewInlineKeyboardAttachmentRequest(kb.Build()))
	}
	api.Messages.AnswerOnCallback(ctx, callbackID, &schemes.CallbackAnswer{Message: body})
}

func popup(callbackID, text string) {
	api.Messages.AnswerOnCallback(ctx, callbackID, &schemes.CallbackAnswer{Notification: text})
}

func notifyAdmins(text string) {
	for _, id := range admins {
		send(id, text, nil)
	}
}

func handle(update schemes.UpdateInterface) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("panic:", r)
		}
	}()

	switch u := update.(type) {
	case *schemes.BotStartedUpdate:
		saveUser(u.User)
		send(u.User.UserId, welcome, menuKb())
	case *schemes.MessageCreatedUpdate:
		if u.Message.Recipient.ChatType == schemes.DIALOG {
			saveUser(u.Message.Sender)
			onText(u.Message.Sender.UserId, strings.TrimSpace(u.Message.Body.Text))
		}
	case *schemes.MessageCallbackUpdate:
		saveUser(u.Callback.User)
		onButton(u.Callback.User.UserId, u.Callback.Payload, u.Callback.CallbackID)
	case *schemes.BotAddedToChatUpdate:
		notifyAdmins(fmt.Sprintf("Бота добавили в канал, chat_id = %d", u.ChatId))
	}
}

func saveUser(u schemes.User) {
	name := u.Name
	if name == "" {
		name = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	exec("INSERT INTO users (user_id, name, created_at) VALUES (?, ?, ?) ON CONFLICT DO UPDATE SET name = excluded.name",
		u.UserId, name, now())
}

func onText(userID int64, text string) {
	if text == "/id" {
		send(userID, fmt.Sprintf("Твой id: %d", userID), nil)
		return
	}
	if strings.HasPrefix(text, "/") && text != "/start" && slices.Contains(admins, userID) {
		admin(userID, text)
		return
	}
	if winnerID := num("SELECT waiting_name FROM users WHERE user_id = ?", userID); winnerID != 0 && len(strings.Fields(text)) >= 2 {
		n := exec("UPDATE winners SET full_name = ? WHERE id = ? AND user_id = ? AND status = 'sent'", text, winnerID, userID)
		switch {
		case n < 0:
			send(userID, oops, nil)
		case n == 0:
			exec("UPDATE users SET waiting_name = 0 WHERE user_id = ?", userID)
			send(userID, "К сожалению, приз больше недоступен.", nil)
		default:
			exec("UPDATE users SET waiting_name = 0 WHERE user_id = ?", userID)
			send(userID, "Готово! 🎉 Данные получены, скоро с тобой свяжутся.", nil)
			notifyAdmins(fmt.Sprintf("🎉 Победитель %d оставил данные: %s", userID, text))
		}
		return
	}
	send(userID, welcome, menuKb())
}

func onButton(userID int64, payload, cb string) {
	action, arg, _ := strings.Cut(payload, ":")
	id, _ := strconv.ParseInt(arg, 10, 64)
	p := getPartner("WHERE raffle_date = ?", today())

	switch {
	case action == "menu":
		reply(cb, welcome, menuKb())
	case action == "how":
		reply(cb, howTo, maxbot.InlineKeyboard(btnRaffle, btnMenu))
	case action == "rules":
		reply(cb, rules, maxbot.InlineKeyboard(btnMenu))
	case action == "chances":
		reply(cb, myChances(userID), maxbot.InlineKeyboard(btnRaffle, btnMenu))
	case action == "claim":
		if num("SELECT COUNT(*) FROM winners WHERE id = ? AND user_id = ? AND status = 'sent'", id, userID) == 0 {
			popup(cb, "Приз недоступен")
			return
		}
		if exec("UPDATE users SET waiting_name = ? WHERE user_id = ?", id, userID) < 0 {
			popup(cb, oops)
			return
		}
		reply(cb, "Напиши <b>имя и фамилию</b>.\n\nОтправляя данные, ты соглашаешься на их передачу партнеру / билетному оператору для вручения приза.", nil)
	case p == nil:
		reply(cb, "Сегодня партнерского дня нет 🎪 Скоро на сцену выйдет новый партнер!", maxbot.InlineKeyboard(btnMenu))
	case action == "promo":
		promo(userID, p, cb)
	case action == "check" && id != p.ID:
		reply(cb, "Этот розыгрыш уже завершен. Загляни в меню — там партнер сегодняшнего дня.", maxbot.InlineKeyboard(btnMenu))
	case num("SELECT COUNT(*) FROM entries WHERE partner_id = ? AND user_id = ?", p.ID, userID) > 0:
		reply(cb, "Ты уже участвуешь в сегодняшнем розыгрыше ✅\nРезультаты сообщим после подведения итогов.", maxbot.InlineKeyboard(btnMenu))
	case num("SELECT COUNT(*) FROM winners WHERE partner_id = ?", p.ID) > 0:
		reply(cb, "Розыгрыш призов от "+p.Name+" уже проведен. Возвращайся завтра — на сцену выйдет новый партнер.", maxbot.InlineKeyboard(btnMenu))
	case action == "raffle":
		reply(cb, "Чтобы попасть в розыгрыш призов от "+p.Name+", подпишись на аккаунт партнера.", subscribeKb(p, false))
	case action == "check":
		checkSubscription(userID, p, cb)
	}
}

func subscribeKb(p *Partner, again bool) *maxbot.Keyboard {
	sub, check := "Подписаться на "+p.Name, "Проверить подписку"
	if again {
		sub, check = "Подписаться", "Проверить еще раз"
	}
	return maxbot.InlineKeyboard(maxbot.Row(maxbot.BtnLink(sub, p.ChannelURL)), maxbot.Row(maxbot.Btn(check, fmt.Sprintf("check:%d", p.ID))), btnMenu)
}

func promo(userID int64, p *Partner, cb string) {
	exec("INSERT INTO promo_requests VALUES (?, ?)", p.ID, userID)
	if exec("UPDATE promocodes SET user_id = ? WHERE rowid = (SELECT rowid FROM promocodes WHERE partner_id = ? AND user_id IS NULL LIMIT 1) "+
		"AND NOT EXISTS (SELECT 1 FROM promocodes WHERE partner_id = ? AND user_id = ?)", userID, p.ID, p.ID, userID) < 0 {
		reply(cb, oops, maxbot.InlineKeyboard(btnMenu))
		return
	}
	code := str("SELECT code FROM promocodes WHERE partner_id = ? AND user_id = ?", p.ID, userID)
	if code == "" {
		reply(cb, "Промокоды от "+p.Name+" уже закончились — их разобрали быстрее всех 🔥\n\n"+
			"Но ты все еще можешь подписаться на партнера и принять участие в розыгрыше призов дня.", maxbot.InlineKeyboard(btnRaffle, btnMenu))
		return
	}
	if execTx(chanceQuery(userID, p.ID)) != nil {
		reply(cb, oops, maxbot.InlineKeyboard(btnMenu))
		return
	}
	reply(cb, fmt.Sprintf("Твой фестивальный бонус от %s готов 🎁\n\nПромокод: <code>%s</code>\n\nИспользуй его до %s на %s.\n\n"+
		"Теперь можешь принять участие в розыгрыше призов дня.", p.Name, code, p.PromoUntil, p.PromoWhere), maxbot.InlineKeyboard(btnRaffle, btnMenu))
}

func checkSubscription(userID int64, p *Partner, cb string) {
	members, err := api.Chats.GetSpecificChatMembers(ctx, p.ChannelID, []int64{userID})
	if err != nil {
		log.Println("проверка подписки:", err)
		popup(cb, "Не получилось проверить подписку, попробуй позже")
		return
	}
	if !slices.ContainsFunc(members.Members, func(m schemes.ChatMember) bool { return m.UserId == userID }) {
		reply(cb, "Пока не вижу подписку 👀\n\nПодпишись на аккаунт "+p.Name+", вернись в бот и попробуй еще раз.", subscribeKb(p, true))
		return
	}
	if execTx([]any{"INSERT INTO entries VALUES (?, ?, ?)", p.ID, userID, now()}, chanceQuery(userID, p.ID)) != nil {
		popup(cb, oops)
		return
	}
	reply(cb, fmt.Sprintf("Ты в лайнапе ✅\n\nЗаявка на розыгрыш призов от %s зарегистрирована.\nНакоплено шансов: <b>%d из %d</b>\n\n"+
		"Возвращайся завтра — на сцену выйдет новый партнер.", p.Name, chances(userID), MaxChances), maxbot.InlineKeyboard(btnMenu))
}

func myChances(userID int64) string {
	days := num("SELECT COUNT(*) FROM partners WHERE raffle_date >= ?", today())
	partners := str(`SELECT COALESCE(GROUP_CONCAT(name, ', '), '') FROM partners WHERE id IN
		(SELECT partner_id FROM chances WHERE user_id = ? UNION SELECT partner_id FROM promocodes WHERE user_id = ?)`, userID, userID)
	return fmt.Sprintf("<b>Твой фестивальный пропуск</b> 🎫\nНакоплено шансов: <b>%d из %d</b>\n"+
		"Чем больше дней розыгрыша ты посетишь, тем выше вероятность выиграть главный приз.\n\n"+
		"Пройдено дней: %d\nПартнеры: %s\nОсталось дней: %d\n\n🏆 Главный приз: 10 билетов на Канье Уэста",
		chances(userID), MaxChances, chances(userID), partners, days)
}

func admin(userID int64, text string) {
	command, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	id, err := strconv.ParseInt(arg, 10, 64)
	needID := command == "/draw" || command == "/confirm" || command == "/give" || command == "/winners"

	switch {
	case needID && err != nil, command == "/send" && arg == "":
		send(userID, "Не хватает аргумента. Пример: /draw 1, /confirm 0, /send текст", nil)
	case command == "/draw":
		send(userID, draw(id), nil)
	case command == "/final":
		send(userID, draw(0), nil)
	case command == "/confirm":
		send(userID, confirm(id), nil)
	case command == "/give":
		send(userID, give(id), nil)
	case command == "/winners":
		send(userID, winnersList(id), nil)
	case command == "/stats":
		send(userID, stats(), nil)
	case command == "/send":
		users := ids("SELECT user_id FROM users")
		go func() {
			ok := 0
			for _, u := range users {
				if send(u, arg, nil) == nil {
					ok++
				}
				time.Sleep(50 * time.Millisecond)
			}
			send(userID, fmt.Sprintf("📣 Рассылка завершена: доставлено %d из %d", ok, len(users)), nil)
		}()
	default:
		send(userID, "/draw ID — розыгрыш партнера\n/final — финальный розыгрыш\n/winners ID — список (0 = финал)\n"+
			"/confirm ID — разослать уведомления (0 = финал)\n/give WINNER_ID — отдать приз резервному\n/stats — статистика\n/send ТЕКСТ — рассылка", nil)
	}
}

func draw(partnerID int64) string {
	if num("SELECT COUNT(*) FROM winners WHERE partner_id = ? AND status != 'draft'", partnerID) > 0 {
		return "Победители уже уведомлены"
	}
	var tickets []int64
	need := FinalWinners + FinalReserve
	if partnerID == 0 {
		tickets = ids("SELECT user_id FROM chances")
	} else {
		p := getPartner("WHERE id = ?", partnerID)
		if p == nil {
			return "Партнер не найден"
		}
		tickets = ids("SELECT user_id FROM entries WHERE partner_id = ?", partnerID)
		need = p.WinnersCount
	}
	if len(tickets) == 0 {
		return "Нет участников"
	}

	participants := slices.Compact(slices.Sorted(slices.Values(tickets)))
	var picked []int64
	for len(tickets) > 0 && len(picked) < need {
		winner := tickets[rand.IntN(len(tickets))]
		picked = append(picked, winner)
		tickets = slices.DeleteFunc(tickets, func(u int64) bool { return u == winner })
	}

	result := map[int64]string{}
	for i, u := range picked {
		result[u] = "победитель"
		if partnerID == 0 && i >= FinalWinners {
			result[u] = "резерв"
		}
	}
	file := fmt.Sprintf("draw_%d_%s.csv", partnerID, time.Now().Format("20060102_150405"))
	if err := writeCSV(file, participants, result); err != nil {
		return "Не удалось сохранить выгрузку, розыгрыш не проведен: " + err.Error()
	}

	queries := [][]any{{"DELETE FROM winners WHERE partner_id = ?", partnerID}}
	for _, u := range picked {
		queries = append(queries, []any{"INSERT INTO winners (partner_id, user_id, reserve, status, created_at) VALUES (?, ?, ?, 'draft', ?)",
			partnerID, u, result[u] == "резерв", now()})
	}
	if err := execTx(queries...); err != nil {
		return "Ошибка базы, розыгрыш не сохранен: " + err.Error()
	}

	return winnersList(partnerID) + fmt.Sprintf("\nУчастников: %d\nВыгрузка: %s\n\nРазослать уведомления: /confirm %d\nПеревыбрать: /draw %d",
		len(participants), file, partnerID, partnerID)
}

func writeCSV(file string, users []int64, result map[int64]string) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\ufeff"); err != nil {
		f.Close()
		return err
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	w.Write([]string{"user_id", "Имя", "Результат"})
	for _, u := range users {
		w.Write([]string{fmt.Sprint(u), str("SELECT name FROM users WHERE user_id = ?", u), result[u]})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func give(oldID int64) string {
	var partnerID int64
	var reserve bool
	var status, fullName string
	err := db.QueryRow("SELECT partner_id, reserve, status, full_name FROM winners WHERE id = ?", oldID).Scan(&partnerID, &reserve, &status, &fullName)
	if err != nil || reserve || fullName != "" || (status != "sent" && status != "failed") {
		return "Этого победителя нельзя заменить (нет такого, резервный или уже оставил данные)"
	}
	newID := num("SELECT id FROM winners WHERE partner_id = ? AND reserve = 1 AND status = 'draft' ORDER BY id LIMIT 1", partnerID)
	if newID == 0 {
		return "Резервные победители закончились"
	}
	if execTx([]any{"UPDATE winners SET status = 'replaced' WHERE id = ?", oldID}, []any{"UPDATE winners SET reserve = 0 WHERE id = ?", newID}) != nil {
		return oops
	}
	return confirm(partnerID)
}

func winnersList(partnerID int64) string {
	rows, err := db.Query(`SELECT w.id, w.user_id, u.name, w.reserve, w.status, w.full_name FROM winners w
		JOIN users u ON u.user_id = w.user_id WHERE w.partner_id = ? ORDER BY w.id`, partnerID)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	s := "<b>Победители:</b>\n"
	for rows.Next() {
		var id, userID int64
		var name, status, fullName string
		var reserve bool
		rows.Scan(&id, &userID, &name, &reserve, &status, &fullName)
		if reserve {
			status = "резерв"
		}
		s += fmt.Sprintf("#%d %s [%d] — %s %s\n", id, name, userID, status, fullName)
	}
	return s
}

func confirm(partnerID int64) string {
	p := getPartner("WHERE id = ?", partnerID)
	sent, failed := 0, 0
	warning := ""
	for _, id := range ids("SELECT id FROM winners WHERE partner_id = ? AND status IN ('draft', 'failed') AND reserve = 0", partnerID) {
		userID := int64(num("SELECT user_id FROM winners WHERE id = ?", id))
		if exec("UPDATE winners SET status = 'sent' WHERE id = ?", id) < 0 {
			failed++
			continue
		}
		var err error
		if p != nil {
			err = send(userID, fmt.Sprintf("Ты в числе хедлайнеров дня 🔥\n\nТы выиграл %s от %s.\n\nЧтобы получить приз, %s",
				p.Prize, p.Name, p.PrizeInstruction), nil)
		} else {
			err = send(userID, "Твой сентябрь выходит на главную сцену 🔥\nТы выиграл "+FinalPrize+"!\n\n"+
				"Чтобы подтвердить получение приза, нажми кнопку ниже и оставь необходимые контактные данные.",
				maxbot.InlineKeyboard(maxbot.Row(maxbot.Btn("Получить приз", fmt.Sprintf("claim:%d", id)))))
		}
		if err == nil {
			sent++
			continue
		}
		failed++
		if exec("UPDATE winners SET status = 'failed' WHERE id = ?", id) < 0 {
			warning += fmt.Sprintf("⚠️ #%d не получил сообщение, но в базе отмечен как sent\n", id)
		}
	}
	return fmt.Sprintf("Уведомлено: %d, не удалось: %d (повторить: /confirm %d)\n%s\n%s", sent, failed, partnerID, warning, winnersList(partnerID))
}

func stats() string {
	s := fmt.Sprintf("<b>Статистика</b>\nПользователей: %d\n\nНовые по дням:\n", num("SELECT COUNT(*) FROM users"))
	rows, _ := db.Query("SELECT substr(created_at, 1, 10) d, COUNT(*) FROM users GROUP BY d ORDER BY d")
	for rows.Next() {
		var day string
		var n int
		rows.Scan(&day, &n)
		s += fmt.Sprintf("%s: %d\n", day, n)
	}
	rows.Close()
	s += "\nПартнеры:\n"
	for _, id := range ids("SELECT id FROM partners ORDER BY raffle_date") {
		total := num("SELECT COUNT(*) FROM promocodes WHERE partner_id = ?", id)
		issued := num("SELECT COUNT(user_id) FROM promocodes WHERE partner_id = ?", id)
		s += fmt.Sprintf("#%d %s: запросов промокода %d, выдано %d, осталось %d, участников %d\n", id,
			str("SELECT name FROM partners WHERE id = ?", id), num("SELECT COUNT(*) FROM promo_requests WHERE partner_id = ?", id),
			issued, total-issued, num("SELECT COUNT(*) FROM entries WHERE partner_id = ?", id))
	}
	return s
}
