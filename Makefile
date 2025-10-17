run:
	go run . -crawl=false -data items.jsonl -out merged_output -addr :8080

sync_kiddo:
	go run . -crawl=true -data items.jsonl -out merged_output -addr :8080

sync_wsfun:
	go run . \
		-crawl=false \
		-crawl_wsfun=true \
		-wsf_cat "https://www.worksheetfun.com/category/grades/preschool/page/1" \
		-wsf_data wsfun_items.jsonl \
		-wsf_cp wsfun.checkpoint.json \
		-delay 1200 \
		-max 0
	
merge_items:
	cat kiddo_items.jsonl wsfun_items.jsonl > items.jsonl

clean:
	rm -f kiddo merged_output