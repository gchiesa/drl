# ─── ECS Cluster ────────────────────────────────────────────────────────────────

resource "aws_ecs_cluster" "main" {
  name = var.cluster_name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name       = aws_ecs_cluster.main.name
  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
  }
}

# ─── Cloud Map (DRL peer discovery via DNS) ──────────────────────────────────────

resource "aws_service_discovery_private_dns_namespace" "drl" {
  name        = "drl.local"
  description = "Private namespace for DRL memberlist peer discovery"
  vpc         = aws_vpc.main.id
}

resource "aws_service_discovery_service" "drl" {
  name = "drl"

  dns_config {
    namespace_id   = aws_service_discovery_private_dns_namespace.drl.id
    routing_policy = "MULTIVALUE"

    dns_records {
      ttl  = 5
      type = "A"
    }
  }

  health_check_custom_config {
    failure_threshold = 1
  }
}

# ─── IAM ─────────────────────────────────────────────────────────────────────────

data "aws_iam_policy_document" "ecs_task_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "task_execution" {
  name               = "${var.cluster_name}-task-execution-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_task_assume.json
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Allow pulling secrets from SSM Parameter Store for sensitive env vars
resource "aws_iam_role_policy" "task_execution_ssm" {
  name = "ssm-read"
  role = aws_iam_role.task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ssm:GetParameters", "secretsmanager:GetSecretValue"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role" "task" {
  name               = "${var.cluster_name}-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_task_assume.json
}

# ─── SSM Parameters (secrets) ────────────────────────────────────────────────────

resource "aws_ssm_parameter" "drl_private_api_key" {
  name  = "/${var.environment}/drl/private-api-key"
  type  = "SecureString"
  value = var.drl_private_api_key
}

resource "aws_ssm_parameter" "drl_membership_primary_key" {
  name  = "/${var.environment}/drl/membership-primary-key"
  type  = "SecureString"
  value = var.drl_membership_primary_key
}

# ─── CloudWatch Log Group ────────────────────────────────────────────────────────

resource "aws_cloudwatch_log_group" "main" {
  name              = "/ecs/${var.cluster_name}"
  retention_in_days = 7
}

# ─── Task Definition ─────────────────────────────────────────────────────────────

locals {
  envoy_config = file("${path.module}/config/envoy/envoy.yaml")
  drl_config   = file("${path.module}/config/drl/config.kdl")
}

resource "aws_ecs_task_definition" "echo_server" {
  family                   = "${var.cluster_name}-${var.service_name}"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.task_cpu
  memory                   = var.task_memory
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  # Inject config files as environment-variable-encoded strings.
  # In production, prefer mounting from S3 via ECS volume or baking into the image.
  container_definitions = jsonencode([
    # ── echo-server ──────────────────────────────────────────────────────
    {
      name      = "echo-server"
      image     = var.echo_server_image
      essential = true

      portMappings = [{
        containerPort = 8080
        protocol      = "tcp"
      }]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.main.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "echo-server"
        }
      }
    },

    # ── envoy sidecar ─────────────────────────────────────────────────────
    {
      name      = "envoy"
      image     = var.envoy_image
      essential = true

      # Envoy config is written to /tmp/envoy.yaml via entrypoint.
      # The 'command' here inlines the YAML via environment variable to avoid
      # a separate config bucket; for production use a proper volume mount.
      entryPoint = ["/bin/sh", "-c"]
      command    = ["echo \"$ENVOY_CONFIG\" | base64 -d > /tmp/envoy.yaml && envoy -c /tmp/envoy.yaml --log-level info"]

      environment = [
        {
          name  = "ENVOY_CONFIG"
          value = base64encode(local.envoy_config)
        }
      ]

      portMappings = [
        { containerPort = 10000, protocol = "tcp" },
        { containerPort = 9901, protocol = "tcp" } # admin
      ]

      dependsOn = [{
        containerName = "drl"
        condition     = "HEALTHY"
      }]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.main.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "envoy"
        }
      }
    },

    # ── drl sidecar ───────────────────────────────────────────────────────
    {
      name      = "drl"
      image     = var.drl_image
      essential = true

      entryPoint = ["/bin/sh", "-c"]
      command    = ["echo \"$DRL_CONFIG\" | base64 -d > /tmp/config.kdl && /drl serve"]

      environment = [
        {
          name  = "DRL_CONFIG"
          value = base64encode(local.drl_config)
        },
        { name = "DRL_CONFIG_PATH", value = "/tmp/config.kdl" }
      ]

      secrets = [
        {
          name      = "DRL_PRIVATE_API_KEY"
          valueFrom = aws_ssm_parameter.drl_private_api_key.arn
        },
        {
          name      = "DRL_MEMBERSHIP_PRIMARY_KEY"
          valueFrom = aws_ssm_parameter.drl_membership_primary_key.arn
        }
      ]

      portMappings = [
        { containerPort = 8081, protocol = "tcp" }, # gRPC
        { containerPort = 8082, protocol = "tcp" }, # internal API
        { containerPort = 9091, protocol = "tcp" }, # metrics
        { containerPort = 7946, protocol = "tcp" }, # memberlist TCP
        { containerPort = 7946, protocol = "udp" }  # memberlist UDP
      ]

      healthCheck = {
        command     = ["CMD-SHELL", "wget -q --spider http://localhost:9091/health || exit 1"]
        interval    = 10
        timeout     = 5
        retries     = 3
        startPeriod = 15
      }

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.main.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "drl"
        }
      }
    }
  ])
}

# ─── ECS Service ─────────────────────────────────────────────────────────────────

resource "aws_ecs_service" "echo_server" {
  name            = var.service_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.echo_server.arn
  desired_count   = var.service_replicas
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.envoy.arn
    container_name   = "envoy"
    container_port   = 10000
  }

  # Register DRL sidecar in Cloud Map so instances discover each other via DNS
  service_registries {
    registry_arn   = aws_service_discovery_service.drl.arn
    container_name = "drl"
    container_port = 7946
  }

  depends_on = [
    aws_lb_listener.http,
    aws_iam_role_policy_attachment.task_execution
  ]
}
