package main

import "walky/internal/store"

// filesystemCheck is a seam for tests: production always uses the ext4 check,
// tests run on temporary directories that may live on another filesystem.
var filesystemCheck = store.CheckFilesystem
