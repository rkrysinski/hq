package platform

import "runtime"

// OS is the operating system hq is built for: darwin on macOS, linux on WSL
// and on a plain Linux. The release asset hq updates itself from and whether
// it installs the iTerm2 profile follow it; code outside this package asks
// for OS, never runtime.GOOS (#157).
const OS = runtime.GOOS
