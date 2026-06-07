output "alb_dns_name" {
  value       = module.platform.alb_dns_name
  description = "Public DNS of the ALB (point your DNS/ACM cert at this)."
}

output "server_repository_url" { value = module.platform.server_repository_url }
output "consumer_repository_url" { value = module.platform.consumer_repository_url }
output "ecs_cluster_name" { value = module.platform.cluster_name }
output "ecs_service_name" { value = module.app.service_name }
output "queue_url" { value = module.app.queue_url }
output "audit_table_name" { value = module.app.audit_table_name }
output "lambda_function_name" { value = module.app.lambda_function_name }
output "alarms_topic_arn" { value = module.app.alarms_topic_arn }
