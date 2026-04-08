package main

import (
        "context"
        "errors"

        "github.com/gotd/td/telegram/auth"
        "github.com/gotd/td/tg"
)

// AuthFlow управляет процессом авторизации MTProto для конкретного пользователя.
type AuthFlow struct {
	Status       string // "waiting_phone", "waiting_code", "waiting_password", "done", "error"
	Error        string
	ctx          context.Context
	cancel       context.CancelFunc
	phoneChan    chan string
	codeChan     chan string
	passwordChan chan string
}

func NewAuthFlow() *AuthFlow {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuthFlow{
		Status:       "waiting_phone",
		ctx:          ctx,
		cancel:       cancel,
		phoneChan:    make(chan string, 1),
		codeChan:     make(chan string, 1),
		passwordChan: make(chan string, 1),
	}
}

func (a *AuthFlow) Phone(ctx context.Context) (string, error) {
	a.Status = "waiting_phone"
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case phone := <-a.phoneChan:
		return phone, nil
	}
}

func (a *AuthFlow) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	a.Status = "waiting_code"
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-a.codeChan:
		return code, nil
	}
}

func (a *AuthFlow) Password(ctx context.Context) (string, error) {
	a.Status = "waiting_password"
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case pwd := <-a.passwordChan:
		return pwd, nil
	}
}

func (a *AuthFlow) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

func (a *AuthFlow) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign up not supported, please register in official app first")
}

// MemorySessionStorage реализует session.Storage используя байтовый срез (в памяти).
type MemorySessionStorage struct {
	Data []byte
}

func (m *MemorySessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	if m.Data == nil {
		return nil, nil // return clean session
	}
	return m.Data, nil
}

func (m *MemorySessionStorage) StoreSession(ctx context.Context, data []byte) error {
	m.Data = data
	return nil
}
