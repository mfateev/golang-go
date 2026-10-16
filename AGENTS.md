# Repository agent notes

## Go build cache

The Go build cache reached 159 GB during isolate development, and a later tool installation failed with `no space left on device`. Run `./bin/go clean -cache` periodically during extended build and test work, especially after a full `src/all.bash` run. Wait for active Go commands to finish before cleaning the cache. Subsequent builds will need to repopulate it.
