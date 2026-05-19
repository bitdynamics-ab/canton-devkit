package docker

import (
	"fmt"
	"runtime"
)

func remediationDockerInstall() string {
	switch runtime.GOOS {
	case "darwin":
		return "Install Docker Desktop for Mac: https://docs.docker.com/desktop/install/mac-install/"
	case "windows":
		return "Install Docker Desktop for Windows: https://docs.docker.com/desktop/install/windows-install/"
	case "linux":
		return "Install Docker Engine: https://docs.docker.com/engine/install/\nThen ensure `docker` is in your PATH."
	}
	return "Install Docker: https://docs.docker.com/get-docker/"
}

func remediationDaemonNotRunning() string {
	switch runtime.GOOS {
	case "darwin":
		return "Start Docker Desktop from Applications, then re-run this command."
	case "windows":
		return "Start Docker Desktop from the Start menu, then re-run this command."
	case "linux":
		return "Start the Docker daemon: `sudo systemctl start docker`\nEnable on boot: `sudo systemctl enable docker`"
	}
	return "Start the Docker daemon and retry."
}

func remediationDockerPermissions() string {
	switch runtime.GOOS {
	case "linux":
		return "Add your user to the `docker` group, then log out and back in:\n  sudo usermod -aG docker $USER"
	case "darwin", "windows":
		return "Ensure Docker Desktop is running and you are signed in."
	}
	return "Grant your user permission to access the Docker socket."
}

func remediationComposeMissing() string {
	switch runtime.GOOS {
	case "darwin", "windows":
		return "Update Docker Desktop to a recent version; Compose v2 ships built-in."
	case "linux":
		return "Install the Docker Compose plugin:\n  Debian/Ubuntu: sudo apt install docker-compose-plugin\n  RHEL/Fedora:   sudo dnf install docker-compose-plugin"
	}
	return "Install Docker Compose v2: https://docs.docker.com/compose/install/"
}

func remediationPortBusy(port int) string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return fmt.Sprintf("Stop the process bound to port %d. Find it with:\n  lsof -nP -iTCP:%d -sTCP:LISTEN", port, port)
	case "windows":
		return fmt.Sprintf("Stop the process bound to port %d. Find it with:\n  netstat -ano | findstr :%d", port, port)
	}
	return fmt.Sprintf("Free TCP port %d on this host.", port)
}

func remediationDiskUnknown() string {
	return "Could not determine free disk space; ensure at least 10 GB is available before running."
}

func remediationDiskLow() string {
	return "Free space by removing unused Docker data:\n  docker system prune -a -f --volumes\nOr move ~/.canton-devkit to a larger volume."
}

func remediationMemoryUnknown() string {
	return "Could not query Docker memory; ensure the daemon has at least 4 GB available."
}

func remediationMemoryLow() string {
	switch runtime.GOOS {
	case "darwin", "windows":
		return "Increase Docker Desktop's memory in Preferences → Resources → Memory (recommend 4 GB+)."
	case "linux":
		return "Free host RAM or extend swap; Canton + Postgres need ~4 GB."
	}
	return "Increase memory available to the Docker daemon."
}
