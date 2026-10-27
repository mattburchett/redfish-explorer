# redfish-explorer (`rfx`)

A terminal UI for walking a BMC's Redfish API. Point it at an iDRAC, iLO,
XCC, OpenBMC or anything else that speaks Redfish, and browse the whole tree
with the keyboard: every `@odata.id` becomes a link, every payload is rendered
as an aligned, color-coded property tree, and properties can be edited in
place (PATCH) or Actions run (POST), always behind a confirmation screen.

```
 ⬢ redfish-explorer                                        session root@10.90.15.4
 redfish ▸ v1 ▸ Systems ▸ System.Embedded.1
╭─ Links 49 ──────────────────╮╭─ Detail ──────────── HTTP 200 · 437ms · 5.7 KB ╮
│▸ ↑ ..  Systems              ││  System  ComputerSystem v1_5_0                   │
│  Bios                       ││  Health ● OK · State Enabled · Power On          │
│  Boot.BootOptions           ││  ────────────────────────────────────────────────│
│  EthernetInterfaces         ││  Bios          → /redfish/v1/Systems/…/Bios      │
│  Links.Chassis[0]           ││  BiosVersion   2.9.0                             │
│  Links.CooledBy[0]  Fan.1A  ││  Boot                                            │
│  …                          ││    BootSourceOverrideTarget   None               │
╰─────────────────────────────╯╰──────────────────────────────────────────────────╯
 ↵ open  ⌫ back  u up  / filter  g goto  a actions  r raw  ⇥ detail    ? help · q quit
```

## Install

```sh
go install github.com/mattburchett/redfish-explorer/cmd/rfx@latest
```

or build from a checkout:

```sh
make            # -> bin/rfx
make install    # -> $GOPATH/bin/rfx
```

The command is `rfx`; the project keeps the longer, searchable name.

## Usage

```sh
export BMC_USER=root BMC_PASSWORD=…
rfx -h 10.90.15.4                    # straight into the explorer, no prompts
rfx 10.90.15.4                       # same, host as a positional argument
rfx                                  # interactive login form
rfx -h bmc -u root                   # prompts only for the password
rfx -h bmc -u root --password-file ~/.bmcpass --path /redfish/v1/Chassis
```

Anything not supplied on the command line or in the environment is asked for
in the login form; when everything is supplied there is no prompt at all.
Leave the username empty to browse anonymously (most BMCs only expose the
service root that way). Note that `-h` is the host; help is `--help`.

| Flag                 | Env                                    | Meaning                                              |
|----------------------|----------------------------------------|------------------------------------------------------|
| `-h`, `--host`       | `BMC_HOST` (or `REDFISH_HOST`)         | BMC host: `10.0.0.5`, `bmc:8443`, `https://bmc`      |
| `-u`, `--user`       | `BMC_USER` (or `REDFISH_USER`)         | Username                                             |
| `-p`, `--password`   | `BMC_PASSWORD` (or `REDFISH_PASSWORD`) | Password (prefer the env var or `--password-file`)   |
| `--password-file`    | `BMC_PASSWORD_FILE`                    | Read the password from a file                        |
| `--verify-tls`       | `BMC_VERIFY_TLS`                       | Verify the certificate (off by default; BMCs are self-signed) |
| `--basic`            | `BMC_BASIC_AUTH`                       | Force HTTP basic auth instead of a Redfish session    |
| `--path`             | `BMC_PATH`                             | Resource to open first (default `/redfish/v1`)        |
| `--timeout`          |                                        | Per-request timeout (default 30s)                     |
| `-o`, `--output`     |                                        | On quit print the current `path`, `url` or `json` to stdout |
| `-V`, `--version`    |                                        | Print the version                                     |

Authentication creates a Redfish session (`X-Auth-Token`) and deletes it on
exit. If the BMC has no working SessionService the client falls back to basic
auth automatically.

## Keys

| Key                | Action                                                     |
|--------------------|------------------------------------------------------------|
| `↑/k` `↓/j`        | Move through links, or move the cursor in the detail pane  |
| `↵` `l` `→`        | Open the selected link (or the link/value under the cursor)|
| `⌫` `h` `←` `[`    | Back in history; `]` forward                               |
| `u` / `~`          | Up to the parent path / jump to the service root           |
| `g`                | Go to any path or full URL                                  |
| `tab`              | Switch between the links pane and the detail pane          |
| `/`                | Filter links (links pane) or search text (detail pane); `n`/`N` step matches |
| `r`                | Toggle the pretty tree and colorized raw JSON              |
| `R`                | Refresh, bypassing the cache                               |
| `y` / `s`          | Copy the URL to the clipboard / save the JSON to a file    |
| `e`                | Edit the value under the cursor (tab cycles `AllowableValues`) |
| `a`                | Run one of the resource's Actions through a parameter form |
| `P`                | Send a raw PATCH body                                      |
| `?` / `q`          | Help / quit                                                |

Actions open a form with one row per parameter: `←/→` cycle the allowed
values, Enter edits free-text fields, `x` clears one, `s` sends. Parameters
come from the inline `@Redfish.AllowableValues` annotations and, when the BMC
provides one, the action's `@Redfish.ActionInfo` resource (names, types,
required flags). `r` drops to a raw JSON body if you need something the form
can't express.

Every write shows the exact method, path and JSON body first and only sends on
`y`. After a successful write the resource is reloaded so you see the result.
`If-Match` is sent when the BMC supplied an ETag.

## Using it from scripts

`-o` prints where you ended up when you quit, so rfx can act as a picker for
other tools. The UI draws on the terminal directly, so stdout stays clean.

```sh
path=$(rfx -h 10.90.15.4 -o path)        # /redfish/v1/Systems/System.Embedded.1
url=$(rfx -h 10.90.15.4 -o url)          # https://10.90.15.4/redfish/v1/...
rfx -h 10.90.15.4 -o json | jq .Status   # the resource you were looking at
```

`q` quits and prints; ctrl+c aborts with nothing printed and exit code 130.

## Trying it without hardware

```sh
go run ./cmd/mockbmc &                 # http://127.0.0.1:8443, admin / password
rfx -h http://127.0.0.1:8443 -u admin -p password
```

`mockbmc -no-session` disables the session service to exercise the basic-auth
fallback. The mock accepts PATCHes and the `ComputerSystem.Reset` action.

## Development

```sh
make test     # unit tests, including a full edit/action flow against the mock
make lint     # go vet + gofmt check
REDFISH_TEST_HOST=10.0.0.5 REDFISH_TEST_USER=root REDFISH_TEST_PASS=… make live
make vendor    # refresh vendor/ after changing dependencies
```

## License

Apache-2.0. See [LICENSE](LICENSE).
