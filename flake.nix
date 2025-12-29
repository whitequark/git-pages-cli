{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    nix-filter.url = "github:numtide/nix-filter";

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      nix-filter,
      ...
    }@inputs:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;

          overlays = [
            inputs.gomod2nix.overlays.default
          ];
        };

        git-pages-cli = pkgs.buildGoApplication {
          pname = "git-pages-cli";
          version = "0";

          src = nix-filter {
            root = self;

            include = [
              "go.mod"
              "go.sum"
              "main.go"
            ];
          };

          buildInputs = with pkgs; [
            pkgsStatic.musl
          ];

          ldflags = [
            "-linkmode external"
            "-extldflags -static"
            "-s -w"
          ];

          go = pkgs.go_1_25;
          modules = ./gomod2nix.toml;
        };
      in
      {
        formatter = pkgs.nixfmt-tree;

        devShells.default = pkgs.mkShell {
          inputsFrom = [
            git-pages-cli
          ];

          packages = with pkgs; [
            gomod2nix
          ];
        };

        packages = {
          inherit git-pages-cli;
          default = git-pages-cli;
        };
      }
    );
}
