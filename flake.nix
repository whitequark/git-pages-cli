{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    nix-filter.url = "github:numtide/nix-filter";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      nix-filter,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        git-pages-cli = pkgs.buildGo125Module {
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

          vendorHash = "sha256-sn1LHCo02g+c3MshMnePr1GMDtucQ1UMOe4E7yTQSKY=";
        };
      in
      {
        formatter = pkgs.nixfmt-tree;

        devShells.default = pkgs.mkShell {
          inputsFrom = [
            git-pages-cli
          ];
        };

        packages = {
          inherit git-pages-cli;
          default = git-pages-cli;
        };
      }
    );
}
