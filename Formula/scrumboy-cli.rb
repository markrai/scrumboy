class ScrumboyCli < Formula
  desc "Command-line client for the Scrumboy project board"
  homepage "https://github.com/markrai/scrumboy"
  license "AGPL-3.0-only"
  version "3.42.0"

  on_macos do
    on_arm do
      url "https://github.com/markrai/scrumboy/releases/download/v3.42.0/scrumboy-cli_v3.42.0_darwin_arm64.tar.gz"
      sha256 "0d8ca8e9af47ba156cc75a3517b4a098cbb51aa4e85768a5a883f0622a378c74"
    end
    on_intel do
      url "https://github.com/markrai/scrumboy/releases/download/v3.42.0/scrumboy-cli_v3.42.0_darwin_amd64.tar.gz"
      sha256 "d97267250bc3c4ec4e84f2cace8b6812cc21d731feec7b5cdc730a1eb2193ae7"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/markrai/scrumboy/releases/download/v3.42.0/scrumboy-cli_v3.42.0_linux_arm64.tar.gz"
      sha256 "55343e2a491972c740c4a1fafe7d3526af83ad72c9336a500da20f3796cc966e"
    end
    on_intel do
      url "https://github.com/markrai/scrumboy/releases/download/v3.42.0/scrumboy-cli_v3.42.0_linux_amd64.tar.gz"
      sha256 "a7f4445f6cfe3d1baac78da7c84adde512c0d04cd5c2f81592c38a4c56d3905f"
    end
  end

  head do
    url "https://github.com/markrai/scrumboy.git", branch: "main"
    depends_on "go" => :build
  end

  def install
    if build.head?
      system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=HEAD"), "./cmd/scrumboy-cli"
    else
      bin.install "scrumboy-cli"
    end
  end

  test do
    assert_match "scrumboy-cli", shell_output("#{bin}/scrumboy-cli version")
  end
end
