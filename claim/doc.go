/*
Package claim manages data associated with the Installation State Spec
https://github.com/cnabio/cnab-spec/pull/411

There are four types of claim data: Installation, Claim, Result and Output. How they are
stored is not dictated by the spec, however we have selected access patterns
around the lowest common denominator (filesystem). Each implementation has
metadata available that allow for optimizations, for example using queries based
on a foreign key, if the underlying storage systems supports it.

Below is the general layout of the data assuming the filesystem as the storage
layer. Claims are grouped by the name of the installation, and keyed by the claim ID.
Results are grouped by the claim ID and keyed by the result ID. Outputs are grouped
by the result ID and are keyed by the "ResultID-OutputName" to generate a unique key.
The groups allow for querying by storage systems that support it.

installations/
  INSTALLATION
claims/
  INSTALLATION/
    CLAIM_ID
results/
  CLAIM_ID/
    RESULT_ID
outputs/
  RESULT_ID/
    RESULT_ID-OUTPUT_NAME

Example

installations/
  mysql.json
claims/
  mysql/
    01EAZDEPCBPEEHQG9C4AF5X1PY.json (install)
	01EAZDEW0R8MQ0GS5D5EAQA2J9.json (upgrade)
  wordpress/
	01EAZDF3ARH5J2D7A30A8Z9QRW.json (install)
results/
  01EAZDEPCBPEEHQG9C4AF5X1PY/ (mysql - install)
    01EAZDGPM8EQKXA544AHCBMYXH.json (success)
  01EAZDEW0R8MQ0GS5D5EAQA2J9 (mysql - upgrade)
    01EAZDHFZJE34ND6GE3BVPP1JA (success)
  01EAZDF3ARH5J2D7A30A8Z9QRW (wordpress - install)
    01EAZDJ8FPR0CD8BNG8EBBGA0N (running)
outputs/
  01EAZDGPM8EQKXA544AHCBMYXH/
    01EAZDGPM8EQKXA544AHCBMYXH-CONNECTIONSTRING

*/
package claim
