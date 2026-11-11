{ config, lib, modulesPath, pkgs, inputs, ... }:
let
  sporePkgs = inputs.spore.packages.${pkgs.stdenv.hostPlatform.system};
  # Host project list. Written by `spore infect`'s Stage() at infect
  # time with a single entry derived from the operator's --repo, or
  # left as the bundled `{ }` default when infect runs without a repo.
  # `spore infect` ships this flake to /etc/nixos. To add a project,
  # append a line there (`spore project new` prints it), then as root:
  # `nixos-rebuild switch --flake /etc/nixos`.
  sporeProjects = import ./spore-projects.nix;
in
{
  imports = [
    (modulesPath + "/installer/scan/not-detected.nix")
    (modulesPath + "/profiles/qemu-guest.nix")
    ./disk-config.nix
  ] ++ lib.optional (builtins.pathExists ./local.nix) ./local.nix;

  # Rebuilds on the box and agents both run flake commands.
  nix.settings.experimental-features = [ "nix-command" "flakes" ];

  nixpkgs.config.allowUnfreePredicate =
    pkg: builtins.elem (lib.getName pkg) [ "claude-code" ];

  boot.loader.grub = {
    efiSupport = true;
    efiInstallAsRemovable = true;
  };

  services.openssh = {
    enable = true;
    settings.PasswordAuthentication = false;
    settings.KbdInteractiveAuthentication = false;
    settings.PermitRootLogin = "prohibit-password";
  };

  # Operator-facing account. No shell prompt, no sudo, no wheel. SSH
  # in as spore -> the login shell (spore-attach) attaches you to a
  # tmux session and exits when you detach:
  # - `ssh -t spore@<host>`: the coordinator when exactly one runs,
  #   else the default pilot shell with the coordinators listed.
  # - `ssh -t spore@<host> <project>`: that project's coordinator.
  # - `ssh -t spore@<host> coord`: a menu of running coordinators.
  # - command="/usr/local/bin/spore-attach pilot <name>" on a key in
  #   authorized_keys: that key always lands in spore/pilot/<name>.
  #
  # The login shell is the shims package's spore-attach, the same
  # script services.spore-fleet links into /usr/local/bin, so the
  # bash fall-throughs (editor `-c` commands, piped no-TTY logins)
  # apply here too.
  #
  # Authorized keys come from local.nix. Root SSH stays open for
  # emergency reconcile.
  users.users.spore = {
    isNormalUser = true;
    home = "/home/spore";
    shell = "${sporePkgs.shims}/bin/spore-attach";
  };

  environment.systemPackages = [
    sporePkgs.spore
    sporePkgs.shims
  ] ++ (with pkgs; [
    claude-code
    codex
    gh
    git
    rsync
    curl
    gnumake
    htop
    tmux
    vim
  ]);

  # Symlink the six host shims into /usr/local/bin/ so the kernel's
  # hard-coded paths (e.g. tokenmonitor's respawn-pane message and the
  # entries written to /etc/spore/coordinator.env by `spore infect`)
  # keep resolving. Sweeps any non-symlink target left over from an
  # earlier `install -m 0755` so a re-deploy converges.
  system.activationScripts.spore-shims = ''
    install -d -m 0755 /usr/local/bin
    for f in spore-attach spore-coordinator-launch spore-worker-brief \
             spore-fleet-tick spore-greet-coordinator spore-greet-worker; do
      target="${sporePkgs.shims}/bin/$f"
      link="/usr/local/bin/$f"
      if [ ! -L "$link" ] || [ "$(readlink "$link")" != "$target" ]; then
        rm -f "$link"
        ln -s "$target" "$link"
      fi
    done
  '';

  # linger keeps the spore user's systemd --user instance running
  # without an attached login, so the home-manager-rendered fleet
  # reconcile units fire from boot.
  users.users.spore.linger = true;

  # home-manager wiring required by services.spore-fleet, which
  # renders the per-project reconcile units under
  # home-manager.users.spore.systemd.user.{services,timers,paths}.
  home-manager.useGlobalPkgs = true;
  home-manager.useUserPackages = true;
  home-manager.users.spore.home.stateVersion = config.system.stateVersion;

  # Fleet reconciler. Replaces the legacy systemd.services.spore-
  # coordinator + timer pair that ticked /usr/local/bin/spore-fleet-
  # tick: the upstream NixOS module generates one per-project
  # reconcile service + timer + path watcher under home-manager.
  #
  # enable is gated on a non-empty projects attrset so `spore infect`
  # without --repo (no project to host yet) still builds.
  #
  # SPORE_COORDINATOR_AGENT / SPORE_AGENT_BINARY keep the local
  # claude-wrapping shims (spore-coordinator-launch / spore-worker-
  # brief) in the loop; they implement site-local role-seeding,
  # brief-piping, transcript logging, and sentinel-file behavior the
  # upstream kernel does not ship.
  services.spore-fleet = {
    enable = sporeProjects != { };
    user = "spore";
    package = sporePkgs.spore;
    claudeCodePackage = pkgs.claude-code;
    # `spore infect --coordinator-agent codex` needs codex on PATH and
    # the user-level ~/.codex/hooks.json the module renders.
    codex.enable = true;
    hostId = config.networking.hostName;
    projects = sporeProjects;
    extraEnv = {
      SPORE_COORDINATOR_AGENT = "/usr/local/bin/spore-coordinator-launch";
      SPORE_AGENT_BINARY = "/usr/local/bin/spore-worker-brief";
      # Override the upstream module's curated PATH (spore + claude-code
      # + bash + coreutils + git + tmux) so /run/wrappers, /usr/local/bin,
      # and the spore user's nix-profile are visible to the reconciler
      # and to the shims it spawns. extraEnv merges last in the module's
      # Environment block so this wins over the curated default.
      PATH = "/run/wrappers/bin:/run/current-system/sw/bin:/home/spore/.nix-profile/bin:/usr/local/bin:/usr/bin:/bin";
    };
  };

  # spore's tmux baseline (deep scrollback, wheel scroll, Claude Code
  # TUI compat). Enabled explicitly so it renders even before the
  # fleet itself is (no --repo at infect time). default-shell is
  # pinned because spore-attach is the spore user's login shell and
  # cannot run tmux's `-c CMD` for new panes.
  services.spore-fleet.tmux = {
    enable = true;
    defaultShell = "${pkgs.bashInteractive}/bin/bash";
  };

  system.stateVersion = "24.05";
}
