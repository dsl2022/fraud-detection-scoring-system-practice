bucket         = "fraud-signals-tfstate-prod"
key            = "fraud-signals/prod/terraform.tfstate"
region         = "us-east-1"
dynamodb_table = "fraud-signals-tflock"
encrypt        = true
