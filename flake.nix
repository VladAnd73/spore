{
  description = "spore - drop-in harness template for LLM-coding agents";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    claude-code = {
      url = "github:sadjow/claude-code-nix";
    };
  };

  outputs =
    { self, nixpkgs, flake-utils, home-manager, claude-code }:
    let
      perSystem = flake-utils.lib.eachDefaultSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          # go_1_26 in nixpkgs is 1.26.8, but the GO-2026-66xx
          # advisories need the 1.26.9 security release, which nixpkgs
          # has not packaged. go_1_27 (1.27.3, also fixing GO-2026-6809) carries the same fixes.
          # Drop back to pkgs.go / pkgs.buildGoModule once the default
          # Go moves past these advisories.
          # nixpkgs' go_1_27 is 1.27.3; GO-2026-6859 (net/http) needs
          # 1.27.4, so build that from its source tarball until nixpkgs
          # packages it. buildGo127Module binds its own go, so hand it
          # this one too or the shipped binary keeps the old stdlib.
          # Keep the spore-toolchain check's `min` in step.
          go = pkgs.go_1_27.overrideAttrs (_old: {
            version = "1.27.4";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go1.27.4.src.tar.gz";
              hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
            };
          });
          buildGoModule = pkgs.buildGo127Module.override { inherit go; };
          version = pkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);
          commit =
            if self ? rev then self.rev
            else if self ? dirtyRev then self.dirtyRev
            else "unknown";

          spore = buildGoModule {
            pname = "spore";
            inherit version;
            src = ./.;
            subPackages = [ "cmd/spore" ];
            vendorHash = null;
            ldflags = [ "-X=github.com/versality/spore.buildCommit=${commit}" ];
            # Integration tests exec git and tmux directly; without
            # them on PATH the buildGoModule check phase fails in the
            # nix sandbox. See cmd/spore/coordinator_lifecycle_test.go
            # and internal/{fleet,task,lints,bootstrap,hooks}/*_test.go.
            nativeCheckInputs = [ pkgs.git pkgs.tmux ];
            meta = {
              description = "Drop-in harness template for LLM-coding agents.";
              homepage = "https://github.com/versality/spore";
              license = pkgs.lib.licenses.asl20;
              mainProgram = "spore";
              platforms = pkgs.lib.platforms.unix;
            };
          };

          # Host shims plus per-user hook / settings / systemd assets,
          # exposed for downstream nix configs that want to manage
          # /usr/local/bin/spore-* via nix instead of the one-shot
          # scp+install from `spore infect`. See
          # docs/host-nix-snippet.md for the operator-facing config.
          shims = pkgs.runCommand "spore-shims-${version}"
            { src = ./bootstrap/handover; } ''
            mkdir -p $out/bin $out/share/spore/hooks $out/share/spore/systemd
            install -m 0755 $src/spore-attach.sh             $out/bin/spore-attach
            install -m 0755 $src/spore-coordinator-launch.sh $out/bin/spore-coordinator-launch
            install -m 0755 $src/spore-worker-brief.sh       $out/bin/spore-worker-brief
            install -m 0755 $src/spore-fleet-tick.sh         $out/bin/spore-fleet-tick
            install -m 0755 $src/greet-coordinator.sh        $out/bin/spore-greet-coordinator
            install -m 0755 $src/greet-worker.sh             $out/bin/spore-greet-worker
            install -m 0755 $src/spore-with-secrets.sh       $out/bin/spore-with-secrets
            install -m 0755 $src/hooks/block-bg-bash.pl      $out/share/spore/hooks/block-bg-bash.pl
            install -m 0755 $src/hooks/load-state-md.pl      $out/share/spore/hooks/load-state-md.pl
            install -m 0755 $src/hooks/guard-tmux.pl         $out/share/spore/hooks/guard-tmux.pl
            install -m 0644 $src/settings.json               $out/share/spore/settings.json
            install -m 0644 $src/systemd/spore-fleet-reconcile.service $out/share/spore/systemd/spore-fleet-reconcile.service
            install -m 0644 $src/systemd/spore-fleet-reconcile.timer   $out/share/spore/systemd/spore-fleet-reconcile.timer
          '';
        in
        {
          packages = {
            inherit spore shims;
            default = spore;
          };

          apps = {
            spore = {
              type = "app";
              program = "${spore}/bin/spore";
              meta = {
                description = "spore kernel CLI";
                mainProgram = "spore";
              };
            };
            default = {
              type = "app";
              program = "${spore}/bin/spore";
              meta = {
                description = "spore kernel CLI";
                mainProgram = "spore";
              };
            };
            claude-code = {
              type = "app";
              program = "${claude-code.packages.${system}.default}/bin/claude";
              meta = {
                description = "claude-code CLI tracked from sadjow/claude-code-nix";
                mainProgram = "claude";
              };
            };
          };

          devShells.default = pkgs.mkShell {
            packages = [ go ] ++ (with pkgs; [
              golangci-lint
              govulncheck
              gopls
              just
              jq
              nixpkgs-fmt
              tmux
              fzf
              ripgrep
            ]) ++ [
              claude-code.packages.${system}.default
            ];
          };

          checks = {
            go-fmt = pkgs.runCommand "spore-go-fmt"
              {
                nativeBuildInputs = [ go ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              unformatted="$(find . -name '*.go' | sort | xargs gofmt -l)"
              if [ -n "$unformatted" ]; then
                echo "gofmt needed:"
                echo "$unformatted"
                exit 1
              fi
              touch $out
            '';
            go-vet = pkgs.runCommand "spore-go-vet"
              {
                nativeBuildInputs = [ go ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go vet ./...
              touch $out
            '';
            golangci-lint = pkgs.runCommand "spore-golangci-lint"
              {
                nativeBuildInputs = [ go pkgs.golangci-lint ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              golangci-lint run ./...
              touch $out
            '';
            go-test = pkgs.runCommand "spore-go-test"
              {
                nativeBuildInputs = [ go pkgs.git pkgs.just pkgs.util-linux pkgs.perl ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go test ./...
              touch $out
            '';
            spore-lint = pkgs.runCommand "spore-lint"
              {
                nativeBuildInputs = [ go pkgs.git ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              git init -q
              git -c user.email=t@t -c user.name=t add -A
              git -c user.email=t@t -c user.name=t commit -q -m seed
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go run ./cmd/spore lint
              touch $out
            '';
            # Load-bearing artifact gate. buildGoModule binds its own go,
            # so pinning the devshell toolchain alone proves nothing about
            # the shipped binary. Read the Go version embedded in the
            # SHIPPED binary and fail if it is older than the vuln-clear
            # floor (the `go` pin above). A source-only or devshell-only
            # check misses a still-vulnerable artifact.
            spore-toolchain = pkgs.runCommand "spore-toolchain"
              {
                nativeBuildInputs = [ go ];
              } ''
              min="1.27.4"
              ver="$(go version ${spore}/bin/spore \
                | grep -oE 'go[0-9]+(\.[0-9]+)+' | head -1 | sed 's/^go//')"
              echo "artifact go toolchain: $ver (require >= $min)"
              if [ -z "$ver" ]; then
                echo "could not read go toolchain from artifact"
                exit 1
              fi
              if ! printf '%s\n%s\n' "$min" "$ver" | sort -V -C; then
                echo "artifact go $ver is older than required $min"
                exit 1
              fi
              touch $out
            '';
            shims-layout = pkgs.runCommand "spore-shims-layout" { nativeBuildInputs = [ pkgs.jq ]; } ''
              fail=0
              # Every hook script settings.json runs must ship executable.
              for h in $(jq -r '.. | .command? // empty' ${shims}/share/spore/settings.json \
                  | grep -o '/\.claude/hooks/[^ ]*' | sed 's|.*/||'); do
                if [ ! -x "${shims}/share/spore/hooks/$h" ]; then
                  echo "settings.json runs hooks/$h, which the shims package does not ship"
                  fail=1
                fi
              done
              for f in \
                bin/spore-attach \
                bin/spore-coordinator-launch \
                bin/spore-worker-brief \
                bin/spore-fleet-tick \
                bin/spore-greet-coordinator \
                bin/spore-greet-worker \
                bin/spore-with-secrets \
                share/spore/hooks/block-bg-bash.pl \
                share/spore/hooks/load-state-md.pl \
                share/spore/settings.json \
                share/spore/systemd/spore-fleet-reconcile.service \
                share/spore/systemd/spore-fleet-reconcile.timer; do
                if [ ! -e "${shims}/$f" ]; then
                  echo "shims package missing $f"
                  fail=1
                fi
              done
              for f in \
                bin/spore-attach \
                bin/spore-coordinator-launch \
                bin/spore-worker-brief \
                bin/spore-fleet-tick \
                bin/spore-greet-coordinator \
                bin/spore-greet-worker \
                bin/spore-with-secrets \
                share/spore/hooks/block-bg-bash.pl \
                share/spore/hooks/load-state-md.pl; do
                if [ ! -x "${shims}/$f" ]; then
                  echo "shims package $f not executable"
                  fail=1
                fi
              done
              [ "$fail" = 0 ] || exit 1
              touch $out
            '';
          } // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
            nixosModules-spore-fleet = pkgs.testers.runNixOSTest {
              name = "spore-fleet-module";
              nodes.machine = { config, lib, pkgs, ... }: {
                imports = [
                  self.nixosModules.spore-fleet
                  home-manager.nixosModules.home-manager
                ];

                users.users.spore-test = {
                  isNormalUser = true;
                  home = "/home/spore-test";
                  createHome = true;
                  linger = true;
                };

                home-manager.useGlobalPkgs = true;
                home-manager.useUserPackages = true;
                home-manager.users.spore-test.home.stateVersion = config.system.stateVersion;

                services.spore-fleet = {
                  enable = true;
                  user = "spore-test";
                  projectRoot = "/home/spore-test/project";
                  # Stub spore CLI: log every invocation to a per-user
                  # trace file the test inspects after the activation
                  # scripts run, then handle the subset of subcommands
                  # the graceful-deploy hooks invoke.
                  package = pkgs.writeShellScriptBin "spore" ''
                    trace="''${HOME:-/tmp}/spore-stub.log"
                    echo "$*" >> "$trace"
                    case "$1 $2" in
                      "fleet reconcile") echo "stub: reconcile" ; exit 0 ;;
                      "fleet enable") : > "''${HOME}/.local/state/spore/fleet-enabled" ; exit 0 ;;
                      "fleet disable") rm -f "''${HOME}/.local/state/spore/fleet-enabled" ; exit 0 ;;
                      "task tell") shift 2; echo "stub: tell $*" ; exit 0 ;;
                      "install --dest") cp -r ${./bootstrap/skills}/. "$3" ; exit 0 ;;
                      "hooks codex-config") printf '{"hooks":{"stub":"%s"}}\n' "$4" ; exit 0 ;;
                      "migrate --auto")
                        for t in bash find grep tail dirname mkdir cat; do
                          command -v "$t" >> "''${HOME}/spore-migrate-path.log" || { echo "missing $t" >> "''${HOME}/spore-migrate-path.log"; exit 1; }
                        done
                        exit 0 ;;
                      *) echo "stub spore: unknown $*" >&2; exit 1 ;;
                    esac
                  '';
                  claudeCodePackage = pkgs.writeShellScriptBin "claude" "exit 0";
                  codex = {
                    enable = true;
                    package = pkgs.writeShellScriptBin "codex" "exit 0";
                  };
                  gracefulDeploy.timeout = 5;
                  tmux.defaultShell = "${pkgs.bashInteractive}/bin/bash";
                  matters.linear = {
                    enable = true;
                    settings = {
                      team = "MAR";
                      ready_state = "Ready";
                    };
                    credentialFiles.api_key = pkgs.writeText "fake-linear-key" "lin_api_test";
                  };
                };

                # A host's own tmux.conf lines render after spore's
                # baseline, so this override must win at runtime.
                environment.etc."tmux.conf".text = "set -g history-limit 4321\n";

                systemd.tmpfiles.rules = [
                  "d /home/spore-test/project 0750 spore-test users -"
                  "d /home/spore-test/.local 0755 spore-test users -"
                  "d /home/spore-test/.local/state 0755 spore-test users -"
                  "d /home/spore-test/.local/state/spore 0755 spore-test users -"
                ];
              };
              testScript = { nodes, ... }:
                let
                  preScript = nodes.machine.services.spore-fleet.gracefulDeploy.preScript;
                  postScript = nodes.machine.services.spore-fleet.gracefulDeploy.postScript;
                  tmuxShell = nodes.machine.services.spore-fleet.tmux.defaultShell;
                  tmux = "${pkgs.tmux}/bin/tmux";
                in
                ''
                  import datetime as dt

                  # Upper bound for user-manager unit probes. Generous
                  # against slow CI boots, but far below the driver's
                  # 15 minute default so a real regression fails fast.
                  USER_UNIT_TIMEOUT = dt.timedelta(seconds=120)

                  machine.wait_for_unit("multi-user.target")
                  # home-manager installs and starts the user units from
                  # this oneshot (RemainAfterExit) system service. The
                  # lingered user manager can reach default.target before
                  # it has run, so wait on it explicitly. systemd escapes
                  # the hyphen in the username, hence the raw string.
                  machine.wait_for_unit(r"home-manager-spore\x2dtest.service")
                  machine.wait_for_unit("default.target", user="spore-test")

                  # spore-migrate activation: the hook ran at boot and
                  # every tool in the migration contract resolved on
                  # its PATH (the stub exits 1 on the first miss).
                  mpath = machine.succeed("cat /home/spore-test/spore-migrate-path.log")
                  assert "missing" not in mpath, mpath
                  assert mpath.count("/nix/store/") == 7, mpath
                  machine.succeed("grep -qx 'migrate --auto' /home/spore-test/spore-stub.log")
                  # spore-shims activation: every host shim resolves
                  # via /usr/local/bin/spore-* as a symlink into the
                  # shims derivation, and each is executable.
                  for shim in [
                      "spore-attach",
                      "spore-coordinator-launch",
                      "spore-worker-brief",
                      "spore-fleet-tick",
                      "spore-greet-coordinator",
                      "spore-greet-worker",
                  ]:
                      target = machine.succeed(f"readlink /usr/local/bin/{shim}").strip()
                      assert target.startswith("/nix/store/"), \
                          f"{shim} not a /nix/store symlink: {target!r}"
                      machine.succeed(f"test -x /usr/local/bin/{shim}")
                  # A no-command login with piped stdin (VS Code
                  # Remote-SSH) falls through to bash reading stdin.
                  out = machine.succeed("echo 'echo ok' | /usr/local/bin/spore-attach")
                  assert out.strip().endswith("ok"), out
                  # VS Code resolves the user env with `$SHELL -i -l -c`.
                  out = machine.succeed("/usr/local/bin/spore-attach -i -l -c 'echo hi' </dev/null")
                  assert out.strip().endswith("hi"), out
                  # tmux baseline: rendered ahead of host lines, and
                  # the host's own history-limit wins in a live server.
                  conf = machine.succeed("cat /etc/tmux.conf")
                  assert "set -g history-limit 250000" in conf, conf
                  assert "set -g default-shell ${tmuxShell}" in conf, conf
                  assert conf.index("250000") < conf.index("4321"), conf
                  machine.succeed("${tmux} -L t new-session -d")
                  limit = machine.succeed("${tmux} -L t show-options -gv history-limit").strip()
                  assert limit == "4321", limit
                  dod = machine.succeed("${tmux} -L t show-options -gv detach-on-destroy").strip()
                  assert dod == "off", dod
                  machine.succeed("${tmux} -L t kill-server")

                  # Talk to the lingered user instance via systemctl
                  # --machine so the call uses the right XDG_RUNTIME_DIR
                  # without an interactive login.
                  def usercmd(cmd):
                      return f"systemctl --machine=spore-test@.host --user {cmd}"
                  def user_wait(cmd):
                      return machine.wait_until_succeeds(
                          usercmd(cmd), timeout=USER_UNIT_TIMEOUT)
                  # Oneshot; trigger it and assert exit 0 plus that the
                  # timer + path watchers are active. Every probe is a
                  # bounded retry: a one-shot is-active right after boot
                  # raced user-session startup in CI (exit 3, inactive).
                  user_wait("start spore-fleet-reconcile-project.service")
                  user_wait("is-active spore-fleet-reconcile-project.timer")
                  user_wait("is-active spore-fleet-reconcile-flag-project.path")
                  user_wait("is-active spore-fleet-reconcile-tasks-project.path")

                  # The matters.linear option must surface as the
                  # env-var contract the matter loader reads. Inspect
                  # the rendered unit env directly so the wire format
                  # stays pinned.
                  env = machine.succeed(usercmd(
                      "show spore-fleet-reconcile-project.service -p Environment"))
                  for needle in [
                      "SPORE_MATTER_LINEAR__ENABLED=1",
                      "SPORE_MATTER_LINEAR__TEAM=MAR",
                      "SPORE_MATTER_LINEAR__READY_STATE=Ready",
                      # systemd resolves %d to $CREDENTIALS_DIRECTORY at
                      # `show` time, so just pin the suffix.
                      "/matter-linear-api_key",
                  ]:
                      assert needle in env, f"missing {needle!r} in:\n{env}"
                  # `systemctl show -p LoadCredential` returns
                  # [unprintable] for binary values; read the unit
                  # file off disk instead. home-manager renders user
                  # units under ~/.config/systemd/user/.
                  unit = machine.succeed(
                      "cat /home/spore-test/.config/systemd/user/spore-fleet-reconcile-project.service")
                  assert "LoadCredential=matter-linear-api_key:" in unit, unit

                  # codex.enable: codex on the system and reconciler
                  # PATH, ~/.codex/hooks.json rendered by the package's
                  # own `hooks codex-config` with hooks pinned to it.
                  machine.succeed("test -x /run/current-system/sw/bin/codex")
                  assert "-codex/bin" in env, env
                  hooks = machine.succeed("cat /home/spore-test/.codex/hooks.json")
                  assert '"stub":"/nix/store/' in hooks and hooks.strip().endswith('/bin/spore"}}'), hooks

                  # Host-wide skills: one directory link per skill under
                  # Claude Code's and Codex's user scopes, so the scope
                  # dirs stay real and unmanaged siblings survive.
                  for root in ["/home/spore-test/.claude/skills", "/home/spore-test/.agents/skills"]:
                      machine.succeed(f"test -d {root} && test ! -L {root}")
                      for skill in ["diagram", "select-agent", "spore-bootstrap"]:
                          machine.succeed(f"test -L {root}/{skill} && test -f {root}/{skill}/SKILL.md")
                  machine.succeed("test -x /home/spore-test/.agents/skills/diagram/diagram")

                  # Graceful-deploy: pre-script disables the kill-switch
                  # and tells every active worker to wrap up; post-script
                  # re-enables it. With no live tmux sessions the pre-
                  # script exits after the disable + "no active workers"
                  # branch and never blocks on the timeout.
                  machine.succeed("touch /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("chown spore-test:users /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("${preScript}")
                  machine.fail("test -e /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("${postScript}")
                  machine.succeed("test -e /home/spore-test/.local/state/spore/fleet-enabled")

                  # The pre-script must have invoked `spore fleet disable`
                  # via the stub; the trace confirms re-exec as the user
                  # and cwd handling worked.
                  machine.succeed("grep -q 'fleet disable' /home/spore-test/spore-stub.log")
                  machine.succeed("grep -q 'fleet enable' /home/spore-test/spore-stub.log")
                '';
            };
          };

          formatter = pkgs.nixpkgs-fmt;
        });
    in
    perSystem // {
      nixosModules.spore-fleet = { pkgs, lib, ... }: {
        imports = [ ./nixosModules/spore-fleet.nix ];
        services.spore-fleet.package =
          lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.spore;
        services.spore-fleet.shimsPackage =
          lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.shims;
        services.spore-fleet.claudeCodePackage =
          lib.mkDefault claude-code.packages.${pkgs.stdenv.hostPlatform.system}.default;
      };
      nixosModules.default = self.nixosModules.spore-fleet;
    };
}
