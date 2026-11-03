#!/bin/sh
# A service: answers each request line by its arguments. "hold" is answered
# only when "release" comes.
echo "$$" >> "$FAKE_DIRECTORY/starts"
answer() {
	printf '{"id":"%s","name":"answer","data":{"text":"%s"}}\n' "$1" "$2"
	printf '{"id":"%s","name":"done"}\n' "$1"
}
field() {
	printf '%s\n' "$line" | sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p"
}
held=""
while IFS= read -r line; do
	id=$(field id)
	arguments=$(field arguments)
	case "$arguments" in
	hold) held=$id ;;
	release)
		answer "$held" held
		answer "$id" released
		;;
	exit) exit 3 ;;
	bad)
		echo 'not json'
		echo '{"name":"notice","data":{"text":"no id"}}'
		answer "$id" ok
		;;
	bad-effect) printf '{"id":"%s","name":"notice"}\n' "$id" ;;
	long)
		head -c 1100000 /dev/zero | tr '\0' a
		echo
		;;
	*) answer "$id" "$(field command):$arguments" ;;
	esac
done
