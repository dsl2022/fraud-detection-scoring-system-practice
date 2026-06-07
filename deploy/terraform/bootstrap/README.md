# Bootstrap — GitHub OIDC + CI roles

One-time, account-level setup run by a human with admin credentials. Creates
everything the main stack + CI need to exist *before* they can run:

- the **remote state backend** — one S3 bucket per env (versioned, KMS-encrypted,
  public-access-blocked, TLS-only) + a shared DynamoDB lock table, matching
  `../envs/<env>.backend.hcl`; and
- the GitHub OIDC plumbing that lets Actions deploy **without long-lived keys**:
  - an **OIDC identity provider** trusting `token.actions.githubusercontent.com`
    (one per AWS account), and
  - three **IAM roles** the workflows assume via OIDC, each scoped by the token's
    `sub` claim to a specific repo + trigger:

| Role | GitHub secret | Workflow / job | Trusted `sub` |
|---|---|---|---|
| `fraud-signals-gha-plan` | `AWS_PLAN_ROLE_ARN` | infra.yml · plan | `repo:OWNER/REPO:pull_request` |
| `fraud-signals-gha-apply` | `AWS_APPLY_ROLE_ARN` | infra.yml · apply | `repo:OWNER/REPO:environment:{dev,stage,prod}` |
| `fraud-signals-gha-deploy` | `AWS_DEPLOY_ROLE_ARN` | app-deploy.yml | `repo:OWNER/REPO:ref:refs/heads/main` |

This layer is **separate from the main stack on purpose**: the apply/deploy
roles must exist *before* CI can run, so they can't be created by the stack they
manage (same chicken-and-egg as the state bucket in `../envs/README.md`).

## Run it once (human, admin credentials, local state)

```bash
cd deploy/terraform/bootstrap

terraform init
terraform apply \
  -var="github_owner=dsl2022" \
  -var="github_repo=fraud-detection-scoring-system-practice"
```

> Defaults already point at `dsl2022/fraud-detection-scoring-system-practice`;
> override the vars if you renamed the repo or split app/infra into two repos.

## Then add the outputs as GitHub secrets

```bash
terraform output            # copy the three role ARNs

gh secret set AWS_PLAN_ROLE_ARN   -b "$(terraform output -raw plan_role_arn)"
gh secret set AWS_APPLY_ROLE_ARN  -b "$(terraform output -raw apply_role_arn)"
gh secret set AWS_DEPLOY_ROLE_ARN -b "$(terraform output -raw deploy_role_arn)"
gh secret set JWT_SECRET          -b "<your-hs256-signing-secret>"
```

(`JWT_SECRET` is consumed by both workflows as `TF_VAR_jwt_secret`.)

## Notes

- **Permissions:** `apply`/`deploy` use `AdministratorAccess` for the demo; the
  comments in `github-oidc.tf` list the minimal services to scope down to. `plan`
  is `ReadOnlyAccess` + scoped state-backend access only.
- **State:** local `terraform.tfstate` here (no S3 backend) — it holds role ARNs
  + backend resource ids, no secrets. Keep it out of the public repo or store it
  in a private vault. (This layer *creates* the S3 backend, so it can't use it.)
- **Backend buckets** are protected with `prevent_destroy`; `terraform destroy`
  here will refuse until you remove that guard — intentional, so a stray destroy
  can't wipe every env's state.
- **Replaces** the manual `aws s3api ... / aws dynamodb ...` steps that used to
  live in `../envs/README.md`.
- **One provider per account:** if the OIDC provider already exists (e.g. created
  in the console), import it instead of re-creating:
  ```bash
  terraform import aws_iam_openid_connect_provider.github \
    arn:aws:iam::<ACCOUNT_ID>:oidc-provider/token.actions.githubusercontent.com
  ```
