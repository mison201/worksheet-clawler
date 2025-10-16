run:
	go run . -crawl=false -data items.jsonl -out merged_output -addr :8080

sync:
	go run . -crawl=true -data items.jsonl -out merged_output -addr :8080

clean:
	rm -f kiddo

test:
	go test .