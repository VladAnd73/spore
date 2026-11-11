# services.spore-fleet project list for this host.
#
# Bundled default is the empty attrset so the flake builds even when
# `spore infect` runs without --repo. When --repo is supplied, infect's
# Stage() overwrites this file with a single-entry attrset derived from
# the repo basename; later, adding a repo is a one-line append plus, as
# root, `nixos-rebuild switch --flake /etc/nixos`. The switch drains
# every project's running workers; check `spore fleet status` first.
{ }
