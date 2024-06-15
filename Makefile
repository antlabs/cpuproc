all:
	go build ./example/proc/proc.go
	GOOS=darwin GOARCH=amd64 go build -o proc.out example/proc/proc.go