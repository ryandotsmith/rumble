scp main.go aws.go cpimg.go x.html ubuntu@rumblegoods.com:~
ssh ubuntu@rumblegoods.com '/usr/local/go/bin/go build -tags aws -o rumble main.go aws.go cpimg.go'
ssh ubuntu@rumblegoods.com 'sudo systemctl restart rumble'
ssh ubuntu@rumblegoods.com 'sudo systemctl status rumble'
