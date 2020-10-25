## 0.3.0 (2020-10-25)

### Improvements

- Update to Go `1.15`
  [hiddeco/cronjobber#14][#14]
- Embed a default timezone database in the binary
  [hiddeco/cronjobber#14][#14]

[#14]: https://github.com/hiddeco/cronjobber/pull/14

## 0.2.0 (2019-05-10)

### Improvements

- Support host independent timezone database
  [hiddeco/cronjobber#10][#10]
- Log detailed error message on invalid timezone
  [hiddeco/cronjobber#11][#11] 
- Switch to `daemon` user and group as `nobody`
  should only be used as a placeholder for
  "unmapped" users and user ids in NFS tree
  exports
  [hiddeco/cronjobber#10][#10]

[#10]: https://github.com/hiddeco/cronjobber/pull/10
[#11]: https://github.com/hiddeco/cronjobber/pull/11

## 0.1.1 (2019-04-06)

### Fixes

- Use schedule location during earliestTime calculations
  [hiddeco/cronjobber#6][#6]
- Use `timezone` in example and CRD
  [hiddeco/cronjobber#6][#6]

### Improvements

- Mount timezone database from host in example `Deployment`
  [hiddeco/cronjobber#6][#6]

### Thanks

Thanks to @Phuong-Trueanthem for submitting the patches.

[#6]: https://github.com/hiddeco/cronjobber/pull/6

## 0.1.0 (2019-03-06)

First semver release.
