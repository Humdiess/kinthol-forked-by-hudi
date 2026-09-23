package ethol

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

type tgMessage struct {
	MessageID       int64  `json:"message_id"`
	Chat            tgChat `json:"chat"`
	Text            string `json:"text"`
	From            tgUser `json:"from"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgUpdatesResponse struct {
	Ok     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}
