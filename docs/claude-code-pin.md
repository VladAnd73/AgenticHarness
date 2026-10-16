# The Claude Code pin

Claude Code's version is chosen in spore, in one place, on purpose.
`flake.nix` carries a second nixpkgs input named `nixpkgs-claude-code`,
held at one explicit revision and read for exactly one package:

```nix
nixpkgs-claude-code.url =
  "github:NixOS/nixpkgs/55bfb4d7437bd4e09ab7cba58a43bd68eb310044";
```

Everything else spore builds comes from the ordinary `nixpkgs` input.
The two move independently. That is the whole point: before this, a host
took Claude Code from whatever nixpkgs revision its own system flake sat
on, so moving Claude Code meant moving the kernel, systemd and every
other package in the same rebuild.

What the pin feeds:

- `packages.<system>.claude-code`, the package itself.
- `apps.<system>.claude-code`, so `nix run <spore>#claude-code` runs it.
- The devshell's `claude`.
- `services.spore-fleet.claudeCodePackage`, as a `lib.mkDefault`.
- The shipped bootstrap template, `bootstrap/flake/configuration.nix`,
  for both the interactive `claude` on PATH and the fleet unit.

Two guards hold it in place. `checks.<system>.claude-code-version` runs
the built binary and fails if it reports older than the floor in
`flake.nix`. `bootstrap_claude_code_test.go` fails if the shipped
template ever stops taking claude-code from spore, or starts naming it
separately in its two places.

## Why the two places in the template must stay one binding

A host installs Claude Code twice: once in `environment.systemPackages`,
which is the `claude` an operator gets on PATH, and once as
`services.spore-fleet.claudeCodePackage`, which the fleet module puts on
the reconciler unit's PATH. Nothing makes them agree. If they drift, a
smoke check reads one build while workers run the other, and neither
side reports anything wrong. The template binds both to one `claudeCode`
value so they cannot.

One wrinkle worth knowing before you debug a version mismatch:
`claudeCodePackage` has exactly one use in the module, the unit's `PATH`,
and `extraEnv` merges after it. A host that sets `extraEnv.PATH` (this
template does, and so does the deployment it was taken from) replaces
that curated PATH wholesale, which leaves `claudeCodePackage` inert and
makes `environment.systemPackages` the thing that actually decides which
Claude Code a worker runs. Check the rendered unit, not the option:

```sh
grep '^Environment=PATH=' \
  ~/.config/systemd/user/spore-fleet-reconcile-<project>.service
```

If that line contains no `/nix/store/...-claude-code-*` entry, the
version your workers run is the one from `environment.systemPackages`.

## Part A: adopt the pin on an existing host

Operator work, on the machine, as root. Nothing in spore does this for
you, and nothing here needs spore's own repository.

This part assumes a host whose system flake already takes spore as an
input and sets `services.spore-fleet`, with lines that look like:

```nix
environment.systemPackages = with pkgs; [ claude-code ... ];
...
services.spore-fleet.claudeCodePackage = pkgs.claude-code;
```

Adjust the file and line numbers to your host.

The rebuild commands below say `/etc/nixos#<config>`. `<config>` is the
attribute name under `nixosConfigurations` in the host's own flake. It is
often the hostname and it is not required to be, so read the real one off
the flake before you start rather than assuming:

```sh
nix eval --impure --raw --expr \
  'builtins.concatStringsSep " " (builtins.attrNames (builtins.getFlake "/etc/nixos").nixosConfigurations)'
```

On the machine this runbook was written on that prints `spore-bootstrap`
while `hostname` prints `nixos`, so `--flake /etc/nixos#nixos` fails
there. Substitute whatever the command prints wherever `<config>`
appears.

### A0. Record what you are rolling back to

```sh
readlink -f /run/current-system/sw/bin/claude
/run/current-system/sw/bin/claude --version
nix-env --list-generations --profile /nix/var/nix/profiles/system | tail -3
```

Keep the store path and the generation number. Every rollback below ends
at one of them.

### A1. Capture the before baseline

From a spore checkout, with an empty task queue:

```sh
scripts/cc-upgrade-smoke.sh > /tmp/cc-smoke-before.txt
```

The script has no side effects outside its own temp directory, which it
deletes on exit. Check 5 exercises the coordinator-poke write path
against a throwaway state directory rather than the live inbox, and check
6 reconciles a throwaway fleet with an empty queue. Keep the file. It is
the only pre-upgrade reference you will have.

Rollback: none needed, it writes nothing you depend on.

### A2. Confirm the transcript garbage collector is off

`CLAUDE_CODE_TRANSCRIPT_LOCAL_GC` arrived in Claude Code 2.1.296. It can
rewrite a large transcript file in place, and spore's token monitors
parse that file. It is opt-in and off by default. Confirm it is set
nowhere before you upgrade, and leave it off:

```sh
env | grep CLAUDE_CODE_TRANSCRIPT_LOCAL_GC
grep -r CLAUDE_CODE_TRANSCRIPT_LOCAL_GC ~/.claude/settings.json \
  ~/.claude/settings.local.json /etc/nixos/ ~/.config/spore/ 2>/dev/null
systemctl --user show 'spore-fleet-reconcile-*.service' -p Environment \
  | grep CLAUDE_CODE_TRANSCRIPT_LOCAL_GC
```

Four silent results mean it is off. If any of them prints a setting, stop
and decide what to do with it before upgrading; nothing further in this
runbook accounts for it being on.

### A3. Point the system flake's Claude Code at spore's pin

Two edits in the host's `configuration.nix`, and they must land together.
Replace the `claude-code` entry in `environment.systemPackages`:

```nix
environment.systemPackages = [
  inputs.spore.packages.${pkgs.stdenv.hostPlatform.system}.claude-code
] ++ (with pkgs; [ ... ]);   # claude-code removed from this list
```

and delete the override line entirely:

```nix
services.spore-fleet.claudeCodePackage = pkgs.claude-code;   # delete
```

Deleting it is enough. The host's line is an unprefixed definition,
priority 100, and spore's module sets the option with `lib.mkDefault`,
priority 1000, so the host's line wins today purely by being there. With
it gone the module's default is the only definition left and takes over.
Measured on a simulated copy of this host's configuration before
writing this down: with the line present the option evaluated to
`claude-code-2.1.226`, the store path the host was running; with the line
deleted it evaluated to the pinned `claude-code-2.1.296`.

Leave `nixpkgs.config.allowUnfreePredicate` alone if the host has one.
Claude Code is unfree, and spore allows it inside its own nixpkgs
evaluation, so the host's predicate is no longer needed for this package;
it is also harmless and still needed by anything else that reaches for
`pkgs.claude-code`.

Rollback: restore both lines. You have not rebuilt yet, so nothing has
changed on the running system.

### A4. Re-lock spore, and only spore

```sh
cd /etc/nixos
cp flake.lock flake.lock.bak
nix flake update spore          # older nix: nix flake lock --update-input spore
```

This re-reads spore's inputs and brings in the `nixpkgs-claude-code`
node. It does not touch the host's own `nixpkgs`. Confirm that before
going further:

```sh
diff <(jq -r '.nodes[.nodes.root.inputs.nixpkgs].locked.rev' flake.lock.bak) \
     <(jq -r '.nodes[.nodes.root.inputs.nixpkgs].locked.rev' flake.lock)
```

Silence means the whole-system nixpkgs did not move. If the host's flake
sets `spore.inputs.nixpkgs-claude-code.follows`, remove it: a `follows`
there would point the pin back at the host's nixpkgs and undo everything
this runbook does. A `spore.inputs.nixpkgs.follows = "nixpkgs"` is fine
and expected; it affects everything except the pin.

Rollback: `cp flake.lock.bak flake.lock`.

### A5. Build without switching, and check the version

```sh
nixos-rebuild build --flake /etc/nixos#<config>
./result/sw/bin/claude --version
```

Expect 2.1.296 or newer. `build` writes `./result` and changes nothing
about the running system.

This is the step that adds a second Claude Code closure to the machine,
so budget disk space and download time here and not only in Part B.
Measured on this host: the pinned build shares not one store path with
the Claude Code the host runs today. All 31 paths are new, about 349 MB,
with their own glibc, gcc and OpenSSL underneath. The two stay disjoint
for as long as the pin and the host's nixpkgs differ, which is the pin
working as designed rather than a one-off cost.

One fact to have rather than discover: this pin moves claude-code's
runtime OpenSSL backwards, from 3.6.3 in the closure the host carries
today to 3.5.8 in the pinned one. The pinned revision is the newer
nixpkgs of the two; it simply happens to resolve `openssl` to an older
release than the host's does. Nothing in spore reads that library
directly and the practical risk is low, but a version going backwards is
exactly what decoupling two nixpkgs makes possible, and nothing
downstream will flag it for you.

Rollback: `rm result`.

### A6. Switch

```sh
nixos-rebuild switch --flake /etc/nixos#<config>
```

Rollback, in increasing order of bluntness:

```sh
nixos-rebuild switch --rollback
# or pick the generation you recorded in A0:
/nix/var/nix/profiles/system-<N>-link/bin/switch-to-configuration switch
```

### A7. Verify

```sh
readlink -f /run/current-system/sw/bin/claude      # the new store path
claude --version                                   # 2.1.296 or newer
grep '^Environment=PATH=' \
  ~/.config/systemd/user/spore-fleet-reconcile-<project>.service
```

Then run the smoke check against the new binary and diff it against A1:

```sh
scripts/cc-upgrade-smoke.sh > /tmp/cc-smoke-after.txt
diff /tmp/cc-smoke-before.txt /tmp/cc-smoke-after.txt
```

Five lines always differ and none of them is a verdict: the version, the
bundle store path, the transcript filename the check happened to pick,
the state.md payload size, and the reconcile duration in milliseconds.
What matters is that no line flips from `OK` to `<-- CHANGED`, and that
the script still exits 0. A `CHANGED` line names the hook whose contract
moved; read it against the Claude Code release notes before trusting
that hook again.

Running workers keep the old binary until they are restarted, because
their process already resolved it. Let them finish or restart the fleet.

## Part B: the whole-system nixpkgs bump

Separate operation, separate day, separate decision. Part A does not
need it and does not do it. Carry it out only when you actually want the
kernel, systemd and everything else to move.

```sh
cd /etc/nixos
cp flake.lock flake.lock.bak
nix flake update nixpkgs
nixos-rebuild build --flake /etc/nixos#<config>
nixos-rebuild switch --flake /etc/nixos#<config>
```

What to expect that Part A did not involve: a new kernel, which means a
reboot before the running system matches the built one; a systemd
version change; and a rebuild of nearly every package on the machine, so
budget disk space and time. Claude Code's version is now independent of
this and will not move with it.

Rollback:

```sh
cp flake.lock.bak flake.lock                   # before switching
nixos-rebuild switch --rollback                # after switching
```

If a kernel came with it, reboot and pick the previous generation from
the bootloader menu rather than relying on `--rollback` alone.

## Bumping the pin itself

This is the operation the pin exists to make small.

1. Pick a nixpkgs revision that carries the Claude Code release you
   want. `pkgs/by-name/cl/claude-code/manifest.zst.json` in that
   revision names the version.
2. Edit the `nixpkgs-claude-code.url` revision in `flake.nix`.
3. `nix flake lock` to re-lock it.
4. Raise the `min` floor in `checks.<system>.claude-code-version` if the
   new release is one spore now depends on.
5. `nix build .#claude-code` and run
   `SMOKE_CLAUDE_BIN=<path>/bin/claude scripts/cc-upgrade-smoke.sh`,
   diffed against the previous baseline. That exercises the new binary
   against every hook contract spore leans on, without touching any
   host. See `docs/cc-upgrade-smoke.md`.
6. Ship it, then adopt it on each host with Part A4 onward.

Downstream consumers of `bootstrap/flake/` inherit the pin when they
re-lock their spore input. They do not need their own.
