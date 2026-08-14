# nix/default.nix
#
# Per-system aggregator. flake.nix imports this once per system and re-exports
# `packages`, `devShells`, `apps`, and `checks`. Every concern lives in its own
# module under ./nix and is wired up here with explicit `import`s.
#
{
  pkgs,
  lib,
  microvm,
  nixpkgs,
  src,
}:

let
  constants = import ./constants.nix;

  versions = import ./versions.nix { inherit pkgs; };

  # The injection payload: hello-world PyTorch script seeded into MinIO.
  helloScript = ../assets/hello.py;

  # The single multi-call Go binary. It exposes three role wrappers
  # (geesefs-runc, geesefsd, geesefs-hook) from one store path, so the two
  # names the NixOS modules reference are the same derivation. See
  # ./lib/mkGoBinary.nix and cmd/geesefs-inject/.
  geesefs-inject = import ./lib/mkGoBinary.nix { inherit pkgs lib versions; } { inherit src; };
  geesefs-runc = geesefs-inject;
  geesefsd = geesefs-inject;

  # The microVM runner (NixOS + docker + minio + injection + in-guest test).
  microvmRunner = import ./microvm.nix {
    inherit
      pkgs
      lib
      microvm
      nixpkgs
      constants
      versions
      geesefs-runc
      geesefsd
      helloScript
      ;
  };

  devshell = import ./devshell.nix {
    inherit
      pkgs
      lib
      versions
      constants
      ;
  };

  scripts = import ./scripts.nix {
    inherit
      pkgs
      lib
      versions
      constants
      ;
  };

  tests = import ./tests {
    inherit
      pkgs
      lib
      versions
      constants
      ;
  };

  # Run a Go tool over the module source in a hermetic sandbox. The module is
  # stdlib-only, so GOPROXY=off with no vendor tree works fully offline.
  goCheck =
    name: script:
    pkgs.runCommand name { nativeBuildInputs = [ versions.go ]; } ''
      cp -r ${src} ./src && chmod -R +w ./src
      cd ./src
      export HOME="$(mktemp -d)"
      export GOCACHE="$(mktemp -d)"
      export GOFLAGS=-mod=mod
      export GOPROXY=off
      export CGO_ENABLED=0
      ${script}
      touch "$out"
    '';
in
{
  packages = {
    microvm = microvmRunner;
    inherit geesefs-inject geesefs-runc geesefsd;
    geesefs-static = versions.geesefsStatic;
    fusermount3-static = versions.fusermount3Static;
    default = microvmRunner;
  };

  devShells.default = devshell;

  apps = {
    vm-start = {
      type = "app";
      program = "${scripts.vmStart}/bin/vm-start";
    };
    vm-stop = {
      type = "app";
      program = "${scripts.vmStop}/bin/vm-stop";
    };
    vm-console = {
      type = "app";
      program = "${scripts.vmConsole}/bin/vm-console";
    };
    vm-ssh = {
      type = "app";
      program = "${scripts.vmSsh}/bin/vm-ssh";
    };
    vm-enter = {
      type = "app";
      program = "${scripts.vmEnter}/bin/vm-enter";
    };
    demo = {
      type = "app";
      program = "${scripts.demo}/bin/demo";
    };
    integration-test = {
      type = "app";
      program = "${tests.integrationTest}/bin/integration-test";
    };
    default = {
      type = "app";
      program = "${tests.integrationTest}/bin/integration-test";
    };
  };

  checks = {
    # Fail the flake if any nix source is unformatted.
    nixfmt-check = pkgs.runCommand "nixfmt-check" { nativeBuildInputs = [ versions.nixfmt ]; } ''
      nixfmt --check $(find ${src} -name '*.nix')
      touch $out
    '';

    # Go format / vet / unit-test gates for the injection binary.
    gofmt = goCheck "gofmt" ''
      unformatted="$(gofmt -l .)"
      if [ -n "$unformatted" ]; then
        echo "gofmt: the following files are not formatted:" >&2
        echo "$unformatted" >&2
        exit 1
      fi
    '';
    go-vet = goCheck "go-vet" "go vet ./...";
    go-test = goCheck "go-test" "go test ./...";

    # Guard against drift between the Go contract constants and nix/constants.nix.
    contract-parity = pkgs.runCommand "contract-parity" { } ''
      fail=0
      for lit in geesefs.enabled geesefs.bucket geesefs.mount geesefs.endpoint /models; do
        if ! grep -qF "$lit" ${src}/internal/contract/contract.go; then
          echo "contract-parity: '$lit' missing from internal/contract/contract.go" >&2; fail=1
        fi
        if ! grep -qF "$lit" ${src}/nix/constants.nix; then
          echo "contract-parity: '$lit' missing from nix/constants.nix" >&2; fail=1
        fi
      done
      [ "$fail" -eq 0 ] || exit 1
      touch $out
    '';

    # The injected payload must actually be static: a dynamically-linked binary
    # would fail to exec inside a container that lacks the host's /nix closure.
    # Assert both halves run *and* report as static ELFs (fail closed on drift).
    geesefs-static-smoke =
      pkgs.runCommand "geesefs-static-smoke"
        {
          nativeBuildInputs = [ pkgs.file ];
          geesefs = versions.geesefsStatic;
          fusermount3 = versions.fusermount3Static;
        }
        ''
          "$geesefs/bin/geesefs" --version
          for b in "$geesefs/bin/geesefs" "$fusermount3/bin/fusermount3"; do
            if ! file "$b" | grep -q "statically linked"; then
              echo "geesefs-static-smoke: $b is not statically linked:" >&2
              file "$b" >&2
              exit 1
            fi
          done
          touch $out
        '';

    # The wrapper binaries must at least parse + locate their deps (selftest).
    geesefs-runc-smoke =
      pkgs.runCommand "geesefs-runc-smoke" { nativeBuildInputs = [ geesefs-runc ]; }
        ''
          geesefs-runc --geesefs-selftest
          touch $out
        '';
    geesefsd-smoke = pkgs.runCommand "geesefsd-smoke" { nativeBuildInputs = [ geesefsd ]; } ''
      geesefsd --geesefs-selftest
      touch $out
    '';
  };
}
