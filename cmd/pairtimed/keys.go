// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/objstore"
)

const keysSummary = "Back the server's keys up, encrypted to Karim's age key (backup), or print their public halves (show)"

func init() {
	register(&command{name: "keys", summary: keysSummary, run: runKeys})
}

// envAgeRecipient is Karim's age public key, in secrets.env
// (deploy/secrets.env.example).
const envAgeRecipient = "PAIRTIME_AGE_RECIPIENT"

func runKeys(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(e.stderr, `Usage:
  pairtimed keys backup [-age age1...] [-o FILE] [-upload]
  pairtimed keys show

backup writes /data/keys and PAIRTIME_EMAIL_KEY, as a tar archive
encrypted with age and armored as text, to -o or to standard output;
-upload also stores it in the main bucket under backups/keys/. Karim
decrypts it offline (DESIGN 10.2):

  age -d -i <identity file> pairtime-keys-<time>.tar.age | tar -x

show prints each key's id and public key.
`)
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	switch args[0] {
	case "backup":
		return runKeysBackup(ctx, e, args[1:])
	case "show":
		return runKeysShow(e, args[1:])
	default:
		fmt.Fprintf(e.stderr, "pairtimed keys: unknown subcommand %q; use backup or show\n", args[0])
		return 2
	}
}

func runKeysBackup(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("keys backup", "Back the server's keys up, encrypted to Karim's age key", e)
	load := configFlags(fs, e)
	recipient := fs.String("age", "", "Karim's age public key, age1...; $"+envAgeRecipient+", from the environment or -secrets, unless set")
	out := fs.String("o", "-", `the file to write, never over an existing one; "-" is standard output`)
	upload := fs.Bool("upload", false, "also store the backup in the main bucket, under "+backup.KeyBackupPrefix)
	if code, done := parse(fs, args); done {
		return code
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(e.stderr, "pairtimed keys backup: "+format+"\n", a...)
		return 1
	}
	cfg, err := load()
	if err != nil {
		return fail("%v", err)
	}
	if *recipient == "" {
		*recipient = cfg.Secrets.Get(envAgeRecipient)
	}
	r, err := backup.ParseRecipient(*recipient)
	if err != nil {
		return fail("%v. Pass -age or set %s", err, envAgeRecipient)
	}
	emailKey := cfg.Secrets.Get(config.EmailKey)
	now := time.Now()
	var b bytes.Buffer
	if err := backup.WriteKeyBackup(&b, backup.KeysDir(cfg.DataDir), emailKey, r, now); err != nil {
		return fail("%v", err)
	}

	held := strings.Join(backup.KeyFiles, ", ")
	if emailKey != "" {
		held += " and " + backup.EmailKeyFile
	} else {
		fmt.Fprintf(e.stderr, "pairtimed keys backup: warning: %s isn't set, so the backup holds no email key\n", config.EmailKey)
	}
	name := backup.KeyBackupName(now)
	if *upload {
		objs, err := openBuckets(cfg)
		if err != nil {
			return fail("%v", err)
		}
		if objs == nil {
			return fail("-upload needs object storage, and [object_storage] in %s is off", cfg.Path)
		}
		key := backup.KeyBackupPrefix + name
		if _, err := objs.main.Put(ctx, key, bytes.NewReader(b.Bytes()), objstore.PutOptions{ContentType: "text/plain", IfNoneMatch: true}); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(e.stderr, "stored the backup of %s in %s as %s\n", held, objs.main.Name(), key)
	}
	if *out == "-" {
		if _, err := e.stdout.Write(b.Bytes()); err != nil {
			return fail("%v", err)
		}
	} else {
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fail("%v", err)
		}
		// A backup half written is no backup, and would make the next
		// try refuse the name, so it goes.
		_, err = f.Write(b.Bytes())
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(*out)
			return fail("%v", err)
		}
		fmt.Fprintf(e.stderr, "wrote the backup of %s to %s\n", held, *out)
	}
	fmt.Fprintf(e.stderr, "encrypted to %s; only its private key, which stays offline, can read it. Suggested name: %s\n", strings.TrimSpace(*recipient), name)
	return 0
}

func runKeysShow(e *env, args []string) int {
	fs := newFlagSet("keys show", "Print each key's id and public key", e)
	load := configFlags(fs, e)
	if code, done := parse(fs, args); done {
		return code
	}
	cfg, err := load()
	if err != nil {
		fmt.Fprintf(e.stderr, "pairtimed keys show: %v\n", err)
		return 1
	}
	keys, err := backup.Load(backup.KeysDir(cfg.DataDir))
	if err != nil {
		fmt.Fprintf(e.stderr, "pairtimed keys show: %v\n", err)
		return 1
	}
	pub := keys.Public()
	fmt.Fprintf(e.stdout, "server key (Ed25519)  id %s  public key %s\n", pub.ServerKeyID(), base64.StdEncoding.EncodeToString(pub.Server))
	fmt.Fprintf(e.stdout, "evidence key (P-256)  id %s  public key %s\n", pub.EvidenceKeyID(), base64.StdEncoding.EncodeToString(pub.Evidence.Bytes()))
	return 0
}
