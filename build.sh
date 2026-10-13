go build -o ./build/HAygg
sshpass -p '1' scp -r ./build/HAygg root@192.168.1.181:/www/wwwroot/hay/
