package login

import (
	"context"
	"log/slog"

	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/chatkit/kits/secret"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/pkg/errors"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

// LoginController is the parent controller that Page controls.
type LoginController interface {
	// Hook is called before the state is opened and before Ready is called. It
	// is meant to be called for hooking the handlers.
	Hook(*gtkcord.State)
	// Ready is called once the user has fully logged in. The session given to
	// the controller will have already been opened and have received the Ready
	// event.
	Ready(*gtkcord.State)
	// PromptLogin is called by the login page if the user needs to log in
	// again, either because their credentials are wrong or Discord returns a
	// server error.
	PromptLogin()
}

// Page is the page containing the login forms.
type Page struct {
	*gtk.Box
	Header *gtk.HeaderBar
	Login  *Component

	ctx  context.Context
	ctrl LoginController
}

var pageCSS = lilcss.Applier("login-page", ``)

// NewPage creates a new Page.
func NewPage(ctx context.Context, ctrl LoginController) *Page {
	p := Page{
		ctx:  ctx,
		ctrl: ctrl,
	}

	p.Header = gtk.NewHeaderBar()
	p.Header.AddCSSClass("login-page-header")
	p.Header.SetShowTitleButtons(true)

	p.Login = NewComponent(ctx, &p)
	p.Login.SetVExpand(true)
	p.Login.SetHExpand(true)

	p.Box = gtk.NewBox(gtk.OrientationVertical, 0)
	p.Box.Append(p.Header)
	p.Box.Append(p.Login)
	pageCSS(p)

	return &p
}

// legacyKeyringID is where the token was saved before the app ID changed from
// io.github.dijama.lildisc. The keyring entry is keyed on the app ID, so
// without this every existing install would come up logged out.
const legacyKeyringID = "io.github.dijama.lildisc.secrets"

// LoadKeyring loads the session from the keyring.
func (p *Page) LoadKeyring() {
	p.asyncLoadFromSecrets(migratingKeyring{
		Driver: secret.KeyringDriver(p.ctx),
		legacy: secret.KeyringDriverForID(legacyKeyringID),
	})
}

// migratingKeyring reads from the legacy keyring entry when the current one
// is empty, and copies what it finds forward. The legacy entry is left in
// place, so a downgrade still finds its token.
type migratingKeyring struct {
	secret.Driver
	legacy secret.Driver
}

func (k migratingKeyring) Get(key string) ([]byte, error) {
	b, err := k.Driver.Get(key)
	if !errors.Is(err, secret.ErrNotFound) {
		return b, err
	}

	b, legacyErr := k.legacy.Get(key)
	if legacyErr != nil {
		return nil, err
	}

	if err := k.Driver.Set(key, b); err != nil {
		slog.Warn(
			"cannot copy keyring entry from the old app ID",
			"key", key,
			"err", err)
	} else {
		slog.Info("copied keyring entry from the old app ID", "key", key)
	}
	return b, nil
}

func (p *Page) asyncLoadFromSecrets(driver secret.Driver) {
	p.Login.Loading.Show()
	p.Login.SetSensitive(false)

	done := func() {
		p.Login.Loading.Hide()
		p.Login.SetSensitive(true)
	}

	gtkutil.Async(p.ctx, func() func() {
		b, err := driver.Get("account")
		if err != nil {
			slog.Info(
				"account not found in keyring",
				"err", err)
			return done
		}

		return func() {
			done()
			p.asyncUseToken(string(b))
		}
	})
}

// asyncUseToken connects with the given token. If driver != nil, then the token
// is stored.
func (p *Page) asyncUseToken(token string) {
	state := gtkcord.Wrap(state.New(token))
	p.ctrl.Hook(state)

	gtkutil.Async(p.ctx, func() func() {
		if err := state.Open(p.ctx); err != nil {
			return func() {
				p.ctrl.PromptLogin()
				p.Login.ShowError(errors.Wrap(err, "cannot open session"))
			}
		}

		return func() {
			p.ctrl.Ready(state)
		}
	})
}
