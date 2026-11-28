class ScrumboyCli < Formula
  desc "Command-line client for the Scrumboy project board"
  homepage "https://github.com/markrai/scrumboy"
  license "AGPL-3.0-only"
  version "3.45.0"

  on_macos do
    on_arm do
      url "https://github.com/markrai/scrumboy/releases/download/v3.45.0/scrumboy-cli_v3.45.0_darwin_arm64.tar.gz"
      sha256 "19a563af19bd2980883bb46b60eadb7fbcaed08a137bc296df95956be1797b13"
    end
    on_intel do
      url "https://github.com/markrai/scrumboy/releases/download/v3.45.0/scrumboy-cli_v3.45.0_darwin_amd64.tar.gz"
      sha256 "b0f952acc42a91d259179dd5fa4b208672421a0a18e59ebb6aa32e0a10345830"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/markrai/scrumboy/releases/download/v3.45.0/scrumboy-cli_v3.45.0_linux_arm64.tar.gz"
      sha256 "d51c08323d7b97c860a28eb9e92b6a4b7227edb47554071d1c6c6eb83ed95d86"
    end
    on_intel do
      url "https://github.com/markrai/scrumboy/releases/download/v3.45.0/scrumboy-cli_v3.45.0_linux_amd64.tar.gz"
      sha256 "8527bea59dff1e5c01a6796c8752e813361269c0a4e8ceeca41127f21cff86e8"
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
