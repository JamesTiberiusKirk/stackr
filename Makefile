test:
	go test ./... -v -race
	
stop_kill:
	docker stop $(docker ps -a -q) && docker rm $(docker ps -a -q )
