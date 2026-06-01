# Homebrew formula for canton-devkit.
#
# This file is mirrored to the public canton-devkit-builds repository.
# On every release tag, scripts/update-homebrew-formula.sh rewrites
# `version` and `sha256` to match the freshly-published public tarballs,
# and a maintainer commits the updated formula.
#
# Public install path:
#   brew install --formula https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit-builds/main/Formula/canton-devkit.rb
#
# Formula references the artifacts published by .github/workflows/release.yml
# to the public canton-devkit-builds repository at tag time.

class CantonDevkit < Formula
  desc "Canton DevKit: LocalNet, DAR, contracts, and observability tooling"
  homepage "https://github.com/bitdynamics-ab/canton-devkit-builds"
  license "Apache-2.0"
  version "0.0.0"

  # Multi-arch downloads. Each block matches one of the per-platform
  # tarballs that .github/workflows/release.yml publishes publicly.
  on_macos do
    on_arm do
      url "https://github.com/bitdynamics-ab/canton-devkit-builds/releases/download/v#{version}/canton-devkit_v#{version}_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/bitdynamics-ab/canton-devkit-builds/releases/download/v#{version}/canton-devkit_v#{version}_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "canton-devkit"
    prefix.install "LICENSE"
    prefix.install "README.md"
  end

  test do
    # Smoke-test: binary launches and exposes the localnet command tree.
    # Avoid touching docker (Homebrew CI containers don't have it) by
    # asking for help only.
    assert_match "Canton LocalNet", shell_output("#{bin}/canton-devkit localnet --help")
  end
end
