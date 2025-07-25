check-docker:
	@docker info > /dev/null 2>&1 || (echo "Docker is not running. Please start Docker and try again."; exit 1)

test: check-docker
	go test ./... -v -race

stop_kill:
	@containers=$$(docker ps -aq); \
	if [ -n "$$containers" ]; then \
		docker stop $$containers; \
		docker rm $$containers; \
	else \
		echo "No containers to stop or remove."; \
	fi

clean_images:
	docker images -a --format '{{.Repository}}:{{.Tag}} {{.ID}}' | grep '^stackr_test-' | awk '{print $$2}' | xargs -r docker rmi -f
