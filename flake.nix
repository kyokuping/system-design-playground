{
  description = "Go playground for system design interview exercises";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { nixpkgs, ... }:
    let
      supportedSystems = [
        "aarch64-darwin"
        "x86_64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };

          goDevTools = with pkgs; [
            go
            gopls
            delve
            gotools
            golangci-lint
            go-tools
            lefthook
            uv
          ];
        in
        {
          default = pkgs.mkShell {
            packages = goDevTools;

            shellHook = ''
              echo "Go $(go version | cut -d' ' -f3) development shell"
              if [ ! -d .venv ]; then
                uv venv
              fi
              source .venv/bin/activate
              uv pip install -q langgraph langchain langchain-anthropic
            '';
          };

          tiny_proximity = pkgs.mkShell {
            packages = goDevTools ++ (with pkgs; [
              postgresql
              docker-compose
            ]);

            shellHook = ''
              echo "Go $(go version | cut -d' ' -f3) development shell for tiny_proximity"
              echo "PostgreSQL client tools and docker-compose available"
              if [ ! -d .venv ]; then
                uv venv
              fi
              source .venv/bin/activate
              uv pip install -q langgraph langchain langchain-anthropic
            '';
          };

          tiny_url_shortener = pkgs.mkShell {
            packages = goDevTools;

            shellHook = ''
              echo "Go $(go version | cut -d' ' -f3) development shell for tiny_url_shortener"
              if [ ! -d .venv ]; then
                uv venv
              fi
              source .venv/bin/activate
              uv pip install -q langgraph langchain langchain-anthropic
            '';
          };
        }
      );

      formatter = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        pkgs.nixfmt
      );
    };
}
