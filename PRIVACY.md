# Privacy Policy

**Last updated:** 21 October 2026

Fiscus is a self-hosted, open-source personal finance application. This policy
explains how the operator of this Fiscus deployment handles personal data when
using Fiscus with Enable Banking and, optionally, GoCardless.

## Operator and scope

This deployment is operated by **Alberto Plaza** ("the operator", "I", "me") for
personal use. This policy applies to data accessed through bank connections that
the operator explicitly authorizes.

It does not describe the independent processing performed by your bank, Enable
Banking, GoCardless, or any other third party. Those providers have their own
privacy policies and terms.

## Data accessed

When you authorize a bank connection, Fiscus may access and store:

- Account holder and account names
- IBANs and other account identifiers
- Account balances and currency
- Booked and pending transactions, including amounts, dates, descriptions, and
  counterparty names
- Bank/institution names and connection status
- Consent and session metadata needed to maintain the connection

Fiscus requests read-only account information. It does not initiate payments or
move funds.

## Purpose and legal basis

The data is used only to:

- Display account balances and transactions in the dashboard
- Evaluate cash-flow, duplicate-charge, spending, and liquidity rules
- Send optional alerts to the operator
- Maintain and troubleshoot the authorized bank connection

Where data protection law applies, processing is based on the operator's consent.
Consent can be withdrawn at any time by revoking the connection at the bank or
deleting the connection in Fiscus.

## Storage and location

Bank data is stored in a SQLite database on a private server controlled by the
operator. Data is not sent to the Fiscus software developers, and Fiscus does not
include analytics or telemetry.

If the operator enables Telegram or Discord notifications, alert content may be
sent to those services. The operator is responsible for configuring those
channels securely.

## Sharing

Personal data is not sold or rented. Data may be processed by:

- The bank that holds the account
- Enable Banking or GoCardless, as the authorized account information provider
- The operator's hosting or infrastructure provider
- Telegram or Discord, only if the operator enables those alert channels

These providers process data under their own terms and privacy policies.

## Retention

Data is retained until the operator deletes the relevant connection, deletes the
local database, or removes backups containing the data. Revoking consent at the
bank stops new data from being retrieved, but it does not automatically delete
data already stored by Fiscus.

## Security

The operator takes reasonable measures to protect the deployment, including
restricting access to the server, protecting dashboard credentials, keeping API
keys and private keys confidential, and applying operating-system and
application updates. No system can be guaranteed to be completely secure.

## Your rights

Depending on applicable law, you may have the right to access, correct, delete,
restrict, or object to the processing of your personal data, and to request data
portability. You may also withdraw consent and lodge a complaint with a
supervisory authority.

In Spain, the supervisory authority is the Agencia Española de Protección de
Datos (AEPD), <https://www.aepd.es/>. You may also contact the supervisory
authority in your country of residence.

## Changes

This policy may be updated when the application or its data practices change.
The latest version is always available at this URL.

## Contact

For privacy or data protection questions, contact:

**Alberto Plaza**  
Email: [albertoplazamontesdm@gmail.com](mailto:albertoplazamontesdm@gmail.com)

Source code: <https://github.com/RedRiotTank/Fiscus>
