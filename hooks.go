package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
)

const hookUsage = `usage: grokbox hook <add|ls|rm|test> [flags]

A hook is how the room reaches you instead of you reaching the room. Register
an address that starts something — a Grok Bot routine's webhook, a CI job, any
URL — and the server calls it whenever somebody says your name.

  grokbox hook add <url> --token K   be woken when you are mentioned
  grokbox hook ls                    who in this room is wired up
  grokbox hook test [id]             call it now, to see that it arrives
  grokbox hook rm [id]               stop being woken

Only mentions fire a hook: "@you", or "@all" for everybody at once. Your own
lines never wake you. That is deliberate — a room where every agent woke on
every line would answer each other's answers, and each wake is a real run.

run "grokbox hook <command> -h" for the flags.
`

// aliasList collects repeated --alias flags.
type aliasList []string

func (a *aliasList) String() string { return strings.Join(*a, ",") }

func (a *aliasList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("alias is empty")
	}
	*a = append(*a, v)
	return nil
}

func cmdHook(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, hookUsage)
		return errors.New("which one? add, ls, rm or test")
	}
	switch args[0] {
	case "add", "set":
		return cmdHookAdd(ctx, args[1:])
	case "ls", "list":
		return cmdHookList(ctx, args[1:])
	case "rm", "remove", "del", "delete":
		return cmdHookRemove(ctx, args[1:])
	case "test", "ping":
		return cmdHookTest(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Print(hookUsage)
		return nil
	default:
		fmt.Fprint(os.Stderr, hookUsage)
		return fmt.Errorf("unknown hook command %q", args[0])
	}
}

func cmdHookAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hook add", flag.ContinueOnError)
	var aliases aliasList
	hookURL := fs.String("url", "", "the address to call (default: the first argument)")
	// Not --key: that is already the room's key, one flag up.
	key := fs.String("token", os.Getenv("GROKBOX_HOOK_TOKEN"), "the webhook's key, sent as \"Authorization: Bearer ...\" (env GROKBOX_HOOK_TOKEN)")
	asJSON := fs.Bool("json", false, "print the hook as JSON")
	fs.Var(&aliases, "alias", "another @name that should wake you (repeatable)")
	fs.Usage = usageFor(fs, "hook add", `register an address to be called when you are mentioned.

  grokbox hook add https://api2.cursor.sh/automations/webhook/ID --token crsr_...

Registering again replaces what you had, so a webhook whose key was
regenerated is corrected by running this a second time. The key is sent to
the server and never comes back: it is a credential that starts a run.`)

	// A bare URL as the first argument is the ordinary way to call this.
	t, err := parseTargetAfter(fs, args, hookURL)
	if err != nil {
		return err
	}
	if *hookURL == "" {
		return errors.New("which address? pass the webhook URL, e.g.\n  grokbox hook add https://api2.cursor.sh/automations/webhook/ID --token crsr_...")
	}
	if _, err := proto.CleanHookURL(*hookURL); err != nil {
		return err
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "grokbox: no --token given — the call will carry no Authorization header, which most webhooks refuse")
	}

	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	hk, err := s.cl.AddHook(ctx, *hookURL, *key, aliases)
	if err != nil {
		return hookHint(err)
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(hk)
	}
	fmt.Printf("hook %s — %s will be woken when someone says %s\n", hk.ID, hk.Name, mentionOf(hk))
	fmt.Printf("calling %s\n\n", hostOfURL(hk.URL))
	fmt.Printf("check it reaches you:  grokbox hook test %s\n", hk.ID)
	return nil
}

func cmdHookList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hook ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = usageFor(fs, "hook ls", "list who in this room gets woken when they are mentioned.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	list, err := s.cl.Hooks(ctx)
	if err != nil {
		return hookHint(err)
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no hooks in this room — nobody here gets woken.\nregister one with: grokbox hook add <url> --token KEY")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tWAKES ON\tCALLING\tWOKEN\t")
	for _, h := range list {
		you := ""
		if h.Name == s.cl.Name {
			you = "  (you)"
		}
		fmt.Fprintf(w, "%s\t%s%s\t%s\t%s\t%d%s\t\n",
			h.ID, h.Name, you, mentionOf(h), hostOfURL(h.URL), h.Woken, hookState(h))
	}
	return w.Flush()
}

func cmdHookRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hook rm", flag.ContinueOnError)
	id := fs.String("id", "", "which hook (default: the first argument, or your own)")
	fs.Usage = usageFor(fs, "hook rm", "stop being woken. With no id it removes your own hook.")
	t, err := parseTargetAfter(fs, args, id)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	target, err := resolveHookID(ctx, s, *id)
	if err != nil {
		return err
	}
	if err := s.cl.RemoveHook(ctx, target); err != nil {
		return hookHint(err)
	}
	fmt.Printf("hook %s removed\n", target)
	return nil
}

func cmdHookTest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hook test", flag.ContinueOnError)
	id := fs.String("id", "", "which hook (default: the first argument, or your own)")
	fs.Usage = usageFor(fs, "hook test", "call a hook now, without waiting to be mentioned.\nA hook the server had given up on is given another chance.")
	t, err := parseTargetAfter(fs, args, id)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	target, err := resolveHookID(ctx, s, *id)
	if err != nil {
		return err
	}

	started := time.Now()
	if err := s.cl.TestHook(ctx, target); err != nil {
		return hookHint(err)
	}
	fmt.Printf("hook %s answered in %s — it will be woken when someone says your name\n",
		target, time.Since(started).Round(time.Millisecond))
	return nil
}

// ------------------------------------------------------------------ helpers

// resolveHookID falls back to whichever hook belongs to the caller, so the
// common case — one person, one hook — needs no id at all.
func resolveHookID(ctx context.Context, s *session, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	list, err := s.cl.Hooks(ctx)
	if err != nil {
		return "", hookHint(err)
	}
	for _, h := range list {
		if h.Name == s.cl.Name {
			return h.ID, nil
		}
	}
	return "", errors.New("you have no hook in this room — register one with: grokbox hook add <url> --token KEY")
}

// parseTargetAfter parses the shared target flags, first taking a leading
// positional argument into dst when there is one that is not an invite code.
func parseTargetAfter(fs *flag.FlagSet, args []string, dst *string) (*targetFlags, error) {
	var lead string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[0], "grokbox1-") {
		lead, args = args[0], args[1:]
	}
	t, err := parseTarget(fs, args)
	if err != nil {
		return nil, err
	}
	if lead != "" && *dst == "" {
		*dst = lead
	}
	return t, nil
}

// mentionOf renders the names that wake a hook, the way somebody would type
// them.
func mentionOf(h proto.Hook) string {
	names := append([]string{h.Name}, h.Aliases...)
	for i, n := range names {
		names[i] = "@" + n
	}
	return strings.Join(names, " ")
}

func hookState(h proto.Hook) string {
	switch {
	case h.Broken:
		return "  (given up on — grokbox hook test to retry)"
	case h.Failed > 0:
		return fmt.Sprintf("  (%d failure(s) since the last success)", h.Failed)
	default:
		return ""
	}
}

func hostOfURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// hookHint turns the server's refusals into something actionable.
func hookHint(err error) error {
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Code {
	case 404:
		return fmt.Errorf("%s", ae.Msg)
	case 403:
		return fmt.Errorf("%s — a hook can only be changed by the member it wakes", ae.Msg)
	case 502:
		return fmt.Errorf("%s\n\nthe hook is registered; it is the far end that did not answer. Check the URL and key in the app that issued them", ae.Msg)
	}
	return err
}
