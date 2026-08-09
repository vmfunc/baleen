{
  description = "baleen - filter feeder for music links";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        runtimeDeps = with pkgs; [ yt-dlp ffmpeg ];
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "baleen";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-sK+lektdgWmk/qLxixGET7Ro4ocl5bsQKPD7nQ0NuqQ=";
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            wrapProgram $out/bin/baleen \
              --prefix PATH : ${pkgs.lib.makeBinPath runtimeDeps}
          '';
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [ go gopls golangci-lint ] ++ runtimeDeps;
        };
      });
}
