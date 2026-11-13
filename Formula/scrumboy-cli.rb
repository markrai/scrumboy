class ScrumboyCli < Formula
  desc "Command-line client for the Scrumboy project board"
  homepage "https://github.com/markrai/scrumboy"
  license "AGPL-3.0-only"

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
