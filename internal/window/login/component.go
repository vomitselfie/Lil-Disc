package login

import (
	"context"
	"strings"

	"github.com/diamondburned/adaptive"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/pkg/errors"
	"github.com/vomitselfie/Lil-Disc/chatkit/components/secretdialog"
	"github.com/vomitselfie/Lil-Disc/chatkit/kits/secret"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/window/login/loading"
)

// LoginComponent is the main component in the login page.
type Component struct {
	*gtk.Box
	Inner *gtk.Box

	Loading  *loading.PulsatingBar
	Methods  *Methods
	Bottom   *gtk.Box
	Remember *rememberMeBox
	ErrorRev *gtk.Revealer
	LogIn    *gtk.Button

	ctx  context.Context
	page *Page
}

var componentCSS = lilcss.Applier("login-component", `
	.login-component {
		background: @lil_surface_raised;
		border: 1px solid @lil_border;
		border-radius: {$radius_xl};
		box-shadow: 0 12px 40px @lil_shadow;
		min-width: 280px;
		margin:  12px;
		padding: 0;
	}
	.login-component > *:not(.osd) {
		margin: 0 8px;
	}
	.login-component > *:nth-child(2) {
		margin-top: 6px;
	}
	.login-component > *:first-child {
		margin-top: 8px;
	}
	.login-component > *:not(:first-child) {
		margin-bottom: 4px;
	}
	.login-component > *:last-child {
		margin-bottom: 8px;
	}
	.login-component .adaptive-errorlabel {
		margin-bottom: 8px;
	}
	.login-button {
		background-color: @lil_accent;
		color: @lil_accent_fg;
		border-radius: {$radius_md};
		font-weight: 650;
	}
	.login-with {
		font-weight: bold;
		margin-bottom: 2px;
	}
	.login-decrypt-button {
		margin-left: 4px;
	}
`)

const decryptMsg = `You've previously chosen to remember the token and may have
used a password to encrypt it. This button unlocks that encrypted token and logs
in using it.`

// NewComponent creates a new login Component.
func NewComponent(ctx context.Context, p *Page) *Component {
	c := Component{
		ctx:  ctx,
		page: p,
	}

	c.Loading = loading.NewPulsatingBar(loading.PulseFast | loading.PulseBarOSD)

	loginWith := gtk.NewLabel("Log in with your Discord token")
	loginWith.AddCSSClass("login-with")
	loginWith.SetXAlign(0)

	c.Methods = NewMethods(&c)

	c.Remember = newRememberMeBox(ctx)

	c.ErrorRev = gtk.NewRevealer()
	c.ErrorRev.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	c.ErrorRev.SetRevealChild(false)

	c.LogIn = gtk.NewButtonWithLabel("Log In")
	c.LogIn.AddCSSClass("suggested-action")
	c.LogIn.AddCSSClass("login-button")
	c.LogIn.SetHExpand(true)
	c.LogIn.ConnectClicked(c.login)

	decrypt := gtk.NewButtonWithLabel("Decrypt (?)")
	decrypt.AddCSSClass("login-decrypt-button")
	decrypt.SetSensitive(false)
	decrypt.SetTooltipText(strings.ReplaceAll(decryptMsg, "\n", " "))
	decrypt.ConnectClicked(c.askDecrypt)

	buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	buttonBox.Append(c.LogIn)
	buttonBox.Append(decrypt)

	gtkutil.Async(ctx, func() func() {
		if secret.IsEncrypted(ctx) {
			return func() { decrypt.SetSensitive(true) }
		} else {
			return func() { decrypt.Hide() }
		}
	})

	c.Inner = gtk.NewBox(gtk.OrientationVertical, 0)
	c.Inner.Append(loginWith)
	c.Inner.Append(c.Methods)
	c.Inner.Append(c.Remember)
	c.Inner.Append(c.ErrorRev)
	c.Inner.Append(buttonBox)
	componentCSS(c.Inner)

	c.Box = gtk.NewBox(gtk.OrientationVertical, 0)
	c.Box.AddCSSClass("login-component-outer")
	c.Box.SetHAlign(gtk.AlignCenter)
	c.Box.SetVAlign(gtk.AlignCenter)
	c.Box.Append(c.Loading)
	c.Box.Append(c.Inner)

	return &c
}

// ShowError reveals the error label and shows it to the user.
func (c *Component) ShowError(err error) {
	errLabel := adaptive.NewErrorLabel(err)
	c.ErrorRev.SetChild(errLabel)
	c.ErrorRev.SetRevealChild(true)
}

// HideError hides the error label.
func (c *Component) HideError() {
	c.ErrorRev.SetRevealChild(false)
}

// Login presses the Login button.
func (c *Component) Login() {
	c.LogIn.Activate()
}

func (c *Component) login() {
	c.loginToken(c.Methods.Token.Text())
}

func (c *Component) SetBusy() {
	c.SetSensitive(false)
	c.Loading.Show()
}

func (c *Component) SetDone() {
	c.SetSensitive(true)
	c.Loading.Hide()
}

func (c *Component) loginToken(token string) {
	go func() {
		driver := c.Remember.SecretDriver()
		if driver == nil {
			return
		}

		if err := driver.Set("account", []byte(token)); err != nil {
			app.Error(c.ctx, errors.Wrap(err, "cannot store account as secret"))
		}
	}()

	c.page.asyncUseToken(token)
}

func (c *Component) askDecrypt() {
	secretdialog.PromptPassword(
		c.ctx, secretdialog.PromptDecrypt,
		func(ok bool, enc *secret.EncryptedFile) {
			if ok {
				c.page.asyncLoadFromSecrets(enc)
			}
		},
	)
}

// Methods holds the token entry.
//
// Logging in with an email address, password and 2FA code used to be offered
// as well. It meant LilDisc handled the account's actual password, a far
// larger trust boundary than a token, through a flow Discord changes and
// blocks at will, so it was removed: log in with a token, and let the
// keyring remember it.
type Methods struct {
	*gtk.Box
	Token *FormEntry
}

var methodsCSS = lilcss.Applier("login-methods", `
	.login-methods .login-formentry {
		margin-top: 8px;
	}
`)

// NewMethods creates a new Methods widget.
func NewMethods(c *Component) *Methods {
	m := Methods{}

	m.Token = NewFormEntry("Token")
	m.Token.AddCSSClass("login-form-token")
	m.Token.ConnectActivate(c.Login)
	m.Token.Entry.SetInputPurpose(gtk.InputPurposePassword)
	m.Token.Entry.SetVisibility(false)

	m.Box = gtk.NewBox(gtk.OrientationVertical, 0)
	m.Box.SetVAlign(gtk.AlignStart)
	m.Box.Append(m.Token)

	methodsCSS(m)
	return &m
}

// FormEntry is a widget containing a label and an entry.
type FormEntry struct {
	*gtk.Box
	Label *gtk.Label
	Entry *gtk.Entry
}

var formEntryCSS = lilcss.Applier("login-formentry", ``)

// NewFormEntry creates a new FormEntry.
func NewFormEntry(label string) *FormEntry {
	e := FormEntry{}
	e.Label = gtk.NewLabel(label)
	e.Label.SetXAlign(0)

	e.Entry = gtk.NewEntry()
	e.Entry.SetVExpand(true)
	e.Entry.SetHasFrame(true)

	e.Box = gtk.NewBox(gtk.OrientationVertical, 0)
	e.Box.Append(e.Label)
	e.Box.Append(e.Entry)
	formEntryCSS(e)

	return &e
}

// Text gets the value entry.
func (e *FormEntry) Text() string { return e.Entry.Text() }

// FocusNext navigates to the next widget.
func (e *FormEntry) FocusNext() {
	e.Entry.Emit("move-focus", gtk.DirTabForward)
}

// FocusNextOnActivate binds Enter to navigate to the next widget when it's
// pressed.
func (e *FormEntry) FocusNextOnActivate() {
	e.Entry.ConnectActivate(e.FocusNext)
}

// ConnectActivate connects the activate signal hanlder to the Entry.
func (e *FormEntry) ConnectActivate(f func()) {
	e.Entry.ConnectActivate(f)
}
