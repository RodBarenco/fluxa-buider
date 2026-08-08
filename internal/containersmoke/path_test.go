package containersmoke

import "testing"

func TestContainerExecutablePath(t *testing.T) {
	t.Parallel()

	got, err := containerExecutablePath("/home/user/build/staged", "/home/user/build/staged/game")
	if err != nil {
		t.Fatalf("containerExecutablePath() error = %v", err)
	}
	if got != "/work/game" {
		t.Fatalf("containerExecutablePath() = %q, want /work/game", got)
	}
}

func TestContainerExecutablePathNested(t *testing.T) {
	t.Parallel()

	got, err := containerExecutablePath("/staged", "/staged/sub/dir/game.exe")
	if err != nil {
		t.Fatalf("containerExecutablePath() error = %v", err)
	}
	if got != "/work/sub/dir/game.exe" {
		t.Fatalf("containerExecutablePath() = %q, want /work/sub/dir/game.exe", got)
	}
}

func TestContainerExecutablePathRejectsOutsideDirectory(t *testing.T) {
	t.Parallel()

	if _, err := containerExecutablePath("/staged", "/elsewhere/game"); err == nil {
		t.Fatal("containerExecutablePath() error = nil, want an error for an executable outside directory")
	}
}
