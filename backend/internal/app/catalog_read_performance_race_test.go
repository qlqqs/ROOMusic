//go:build race

package app

// race 构建只验证正确性，不以其额外开销判断正常构建的性能门槛。
const catalogHTTPPerfRaceEnabled = true
