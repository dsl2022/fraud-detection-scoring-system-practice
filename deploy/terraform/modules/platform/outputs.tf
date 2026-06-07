output "cluster_arn" { value = aws_ecs_cluster.this.arn }
output "cluster_name" { value = aws_ecs_cluster.this.name }
output "kms_key_arn" { value = aws_kms_key.this.arn }

output "server_repository_url" { value = aws_ecr_repository.server.repository_url }
output "consumer_repository_url" { value = aws_ecr_repository.consumer.repository_url }

output "app_log_group_name" { value = aws_cloudwatch_log_group.app.name }
output "consumer_log_group_name" { value = aws_cloudwatch_log_group.consumer.name }

output "alb_dns_name" { value = aws_lb.this.dns_name }
output "alb_arn_suffix" { value = aws_lb.this.arn_suffix }
output "target_group_arn" { value = aws_lb_target_group.app.arn }
output "target_group_arn_suffix" { value = aws_lb_target_group.app.arn_suffix }
