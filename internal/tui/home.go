package tui

import "errors"

// Home runs the navigable home menu (mouse + keyboard) and returns the chosen
// action key ("new" / "doctor" / "init" / "quit").
func Home(summary string) (string, error) {
	steps := []Step{{
		Title: "",
		Help:  summary,
		Options: []Choice{
			{Key: "new", Label: "New project", Desc: "scaffold from your profile"},
			{Key: "doctor", Label: "Doctor", Desc: "check host tools"},
			{Key: "init", Label: "Edit defaults", Desc: "change your profile"},
			{Key: "quit", Label: "Quit", Desc: ""},
		},
	}}
	res, err := Wizard("keel", "a web development studio for any stack", steps)
	if err != nil {
		if errors.Is(err, ErrCancelled) {
			return "quit", nil
		}
		return "", err
	}
	return res[0][0], nil
}
