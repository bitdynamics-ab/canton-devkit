# Homebrew formula for canton-devkit.
#
# This file lives in the main repo as the source of truth. On every
# release tag, scripts/update-homebrew-formula.sh rewrites `version`,
# `url`, and `sha256` to match the freshly-published tarballs, and a
# maintainer commits the updated formula.
#
# Two install paths supported:
#
#   1. Direct (no tap):
#      brew install --formula https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/Formula/canton-devkit.rb
#
#   2. Via tap (once bitdynamics-ab/homebrew-canton-devkit exists):
#      brew tap bitdynamics-ab/canton-devkit
#      brew install canton-devkit
#
# Formula references the artifacts published by .github/workflows/release.yml
# at tag time (see docs/packaging.md).

class CantonDevkit < Formula
  desc "Canton DevKit: LocalNet, DAR, contracts, and observability tooling"
  homepage "https://github.com/bitdynamics-ab/canton-devkit"
  license "Apache-2.0"
  version "0.0.0"

  # Multi-arch downloads. Each block matches one of the per-platform
  # tarballs that .github/workflows/release.yml's `binaries` job emits.
  # The release naming convention is locked by BIT-19; update both sides
  # together if it ever changes.
  on_macos do
    on_arm do
      url "https://github.com/bitdynamics-ab/canton-devkit/releases/download/v#{version}/canton-devkit_v#{version}_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/bitdynamics-ab/canton-devkit/releases/download/v#{version}/canton-devkit_v#{version}_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    # Tarball contains: canton-devkit binary + LICENSE + README.md.
    bin.install "canton-devkit"
    prefix.install "LICENSE"
    prefix.install "README.md" if File.exist?("README.md")
  end

  test do
    # Smoke-test: binary launches and exposes the localnet command tree.
    # Avoid touching docker (Homebrew CI containers don't have it) by
    # asking for help only.
    assert_match "Canton LocalNet", shell_output("#{bin}/canton-devkit localnet --help")
  end
end
