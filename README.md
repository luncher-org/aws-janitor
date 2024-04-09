# AWS Janitor

A GitHub Action to cleanup AWS resources.

It uses a mark and delete approach:
- First time it runs, it describes resources and marks them for deletion.
- Next execution, it deletes previously marked resources.

The tag `aws-janitor/marked-for-deletion` is used as deletion marker.

**Any resource that includes the tag key defined by `ignore-tag`, will never be deleted.**

> By default the action will not perform the delete (i.e. it will be a dry-run). You need to explicitly set commit to `true`.

It supports cleaning up the following services:

- EKS Clusters
- Auto Scaling Groups
- Load Balancers
- Security Groups
- CloudFormation Stacks

It follows this strict order to avoid failures caused by inter-resource dependencies. Although intermittent failures may occur, they should be resolved in subsequent executions.

## Inputs

| Name              | Required | Description                                                                                       |
| ----------------- | -------- | ------------------------------------------------------------------------------------------------- |
| regions           | Y        | A comma separated list of regions to clean resources in. You can use * for all regions            |
| allow-all-regions | N        | Set to true if use * from regions.                                                                |
| commit            | N        | Whether to perform the delete. Defaults to `false` which is a dry run                             |
| ignore-tag        | N        | The name of the tag that indicates a resource should not be deleted. Defaults to `janitor-ignore` |

## Example Usage

```yaml
jobs:
  cleanup:
    runs-on: ubuntu-latest
    name: Cleanup resource groups
    steps:
      - name: Cleanup
        uses: rancher-sandbox/aws-janitor@v0.1.0
        with:
            regions: eu-west-1
            ignore-tag: janitor-ignore
        env:
            AWS_ACCESS_KEY_ID: {{secrets.AWS_ACCESS_KEY_ID}}
            AWS_SECRET_ACCESS_KEY: {{secrets.AWS_SECRET_ACCESS_KEY}}
```

## Implementation Notes

The original implementation of the janitor avoided using the mark and delete approach for simplicity but this solution is not viable when supporting deletion on resources that do not have a creation date.
