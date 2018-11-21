resource "aws_ssm_parameter" "database_url" {
  name  = "/${var.vpc_name}/env/DATABASE_URL"
  type  = "SecureString"
  value = "postgres://${aws_db_instance.default.username}:${var.database_password}@${aws_db_instance.default.address}:${aws_db_instance.default.port}/${aws_db_instance.default.name}?sslmode=disable"
}

variable "ssh_public_key" {
  type    = "string"
  default = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCzM2/wM4EzrudXaBKVSD8nT7et43pr5SzeGdyr6mPFZghLmDLoH1Mo55f4hnYZtt9So5JKMLUR1b5Eppc2mUg1j+hQIPtphuA0TRyAWFWfuTS7ppn+a7Up+Kd6DVPFebgvFENfY2BqjyzmkbzC2dPomZL/3oCfid6OPkSLs26oqO7SmbBvGnEjyQGjoN5ev6nzf78ba4mBoidL65PjkzBs3tRwRkAA8dLijvV/7O9PwL6AZPCznv3oy3Pc/URo0GxvuaI7IcrChB+cjJ4TjabsLqQ2YpnLheMLO1EQL8cO5kkFp+viK04qUGcx0InOfEABBrmG680qqMBx9ugAnrsv tf"
}

variable "database_password" {
  type    = "string"
  default = "changeme"
}

variable "vpc_name" {
  type    = "string"
  default = "rumble"
}

variable "cidr_block" {
  type    = "string"
  default = "10.1.0.0/16"
}

provider "aws" {
  version = "~> 1.22"
  region  = "us-west-1"
}

resource "aws_vpc" "default" {
  cidr_block           = "${var.cidr_block}"
  enable_dns_hostnames = true

  tags {
    Name = "${var.vpc_name}"
  }
}

data "aws_availability_zones" "available" {}

resource "aws_internet_gateway" "default" {
  vpc_id = "${aws_vpc.default.id}"
}

resource "aws_route" "internet_access" {
  route_table_id         = "${aws_vpc.default.main_route_table_id}"
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = "${aws_internet_gateway.default.id}"
}

resource "aws_subnet" "default" {
  vpc_id                  = "${aws_vpc.default.id}"
  cidr_block              = "${cidrsubnet(var.cidr_block, 8, 1)}"
  map_public_ip_on_launch = true
  availability_zone       = "${data.aws_availability_zones.available.names[0]}"

  tags {
    Name = "${var.vpc_name}"
  }
}

resource "aws_subnet" "b" {
  vpc_id                  = "${aws_vpc.default.id}"
  cidr_block              = "${cidrsubnet(var.cidr_block, 8, 2)}"
  map_public_ip_on_launch = true
  availability_zone       = "${data.aws_availability_zones.available.names[2]}"

  tags {
    Name = "${var.vpc_name}-b"
  }
}

resource "aws_security_group" "app" {
  vpc_id      = "${aws_vpc.default.id}"
  name        = "${var.vpc_name}"
  description = "SSH + Peer traffic"

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Public SSH access"
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Public peer access"
  }

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Should redirect to https"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "All outgoing traffic allowed"
  }
}

data "aws_ami" "ubuntu" {
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-xenial-16.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  owners = ["099720109477"] # Canonical
}

resource "aws_iam_role" "default" {
  name = "${var.vpc_name}"

  assume_role_policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Principal": {
        "Service": "ec2.amazonaws.com"
      },
      "Effect": "Allow",
      "Sid": ""
    }
  ]
}
EOF
}

resource "aws_iam_instance_profile" "default" {
  name = "${var.vpc_name}-app-server"
  role = "${aws_iam_role.default.name}"
}

resource "aws_iam_role_policy" "default" {
  name = "${var.vpc_name}-app-server"
  role = "${aws_iam_role.default.id}"

  policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ssm:Describe*",
        "ssm:Get*",
        "ssm:List*"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": "s3:*",
      "Resource": "${aws_s3_bucket.imgs.arn}"
    },
    {
      "Effect": "Allow",
      "Action": "ec2:DescribeTags",
      "Resource": "*"
    }
  ]
}
EOF
}

resource "aws_key_pair" "default" {
  key_name   = "${var.vpc_name}-deployer-key"
  public_key = "${var.ssh_public_key}"
}

resource "aws_instance" "default" {
  connection {
    user = "ubuntu"
  }

  ami                    = "${data.aws_ami.ubuntu.id}"
  instance_type          = "t3.small"
  vpc_security_group_ids = ["${aws_security_group.app.id}"]
  subnet_id              = "${aws_subnet.default.id}"
  key_name               = "${aws_key_pair.default.key_name}"
  iam_instance_profile   = "${aws_iam_instance_profile.default.id}"

  root_block_device = {
    volume_size = 100
  }

  tags {
    Name = "${var.vpc_name}-1"
  }

  provisioner "local-exec" "build-server" {
    working_dir = "/Users/r/src/rumble"

    environment {
      GOARCH = "amd64"
      GOOS   = "linux"
    }

    command = "go build -o /tmp/rumble x.go"
  }

  provisioner "file" {
    source      = "/tmp/rumble"
    destination = "/home/ubuntu/rumble"
  }

  provisioner "file" {
    source      = "./rumble.service"
    destination = "/home/ubuntu/rumble.service"
  }

  provisioner "file" {
    source      = "./rumble.socket"
    destination = "/home/ubuntu/rumble.socket"
  }

  provisioner "remote-exec" {
    inline = [
      "chmod +x /home/ubuntu/rumble",
      "sudo mv /home/ubuntu/rumble.service /etc/systemd/system/",
      "sudo mv /home/ubuntu/rumble.socket /etc/systemd/system/",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable rumble.socket",
      "sudo systemctl enable rumble",
      "sudo systemctl start rumble.socket",
    ]
  }
}

resource "aws_db_subnet_group" "default" {
  name       = "${var.vpc_name}"
  subnet_ids = ["${aws_subnet.default.id}", "${aws_subnet.b.id}"]
}

resource "aws_security_group" "postgresql" {
  vpc_id = "${aws_vpc.default.id}"
  name   = "${var.vpc_name}-pg"

  ingress {
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "postgres access"
  }
}

resource "aws_db_instance" "default" {
  identifier = "${var.vpc_name}-postgres"
  name       = "${var.vpc_name}"

  username               = "su"
  password               = "${var.database_password}"
  publicly_accessible    = true
  vpc_security_group_ids = ["${aws_security_group.postgresql.id}"]
  db_subnet_group_name   = "${aws_db_subnet_group.default.id}"
  port                   = "5432"
  multi_az               = false

  engine            = "postgres"
  engine_version    = "10.4"
  instance_class    = "db.m4.large"
  allocated_storage = "100"         #GB
  iops              = "1000"
  storage_encrypted = true

  backup_retention_period    = "7"                        #days
  backup_window              = "04:00-04:30"              #UTC
  maintenance_window         = "sun:04:30-sun:05:30"      #UTC
  auto_minor_version_upgrade = false
  final_snapshot_identifier  = "${var.vpc_name}-postgres"
  skip_final_snapshot        = false
  copy_tags_to_snapshot      = false
}

resource "aws_eip" "default" {
  instance = "${aws_instance.default.id}"
  vpc      = true
}

resource "aws_route53_record" "rumblegoods" {
  zone_id = "ZOK8AI80EBNS3"
  name    = "rumblegoods.com"
  type    = "A"
  ttl     = "60"
  records = ["${aws_eip.default.public_ip}"]
}

resource "aws_s3_bucket" "imgs" {
  bucket = "imgs.rumblegoods.com"
  acl    = "public-read"

  website {
    index_document = "index.html"
  }

  policy = <<EOF
{
  "Version":"2008-10-17",
  "Statement":[{
    "Sid":"AllowPublicRead",
    "Effect":"Allow",
    "Principal": {"AWS": "*"},
    "Action":["s3:GetObject"],
    "Resource":["arn:aws:s3:::imgs.rumblegoods.com/*"]
  }]
}
EOF
}

resource "aws_route53_record" "imgs" {
  zone_id = "ZOK8AI80EBNS3"
  name    = "imgs.rumblegoods.com"
  type    = "A"

  alias {
    name    = "${aws_s3_bucket.imgs.website_domain}"
    zone_id = "${aws_s3_bucket.imgs.hosted_zone_id}"

    evaluate_target_health = false
  }
}
