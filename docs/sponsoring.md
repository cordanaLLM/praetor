# 💖 Polar.sh Issue Bounties & Contributor Rewards

Praetor uses **Polar.sh** as Merchant of Record (MoR) to link transparent feature bounties, issue funding, and contributor reward splits directly to its GitHub roadmap.

> Funding accounts are operator configuration. The README badges, `.github/FUNDING.yml`, this site's social links and bounty announcement, and the bounty board at the end of this page list only the accounts named in `.config/operator/funding.yaml`; see [operational configuration](guides/operational-configuration.md#funding-example).

---

## 🎯 How Polar.sh Rewards Work

With Polar.sh, sponsors, enterprises, and community members can pledge funds directly to concrete GitHub issues or feature requests.

```text
┌─────────────────────────┐      ┌─────────────────────────┐      ┌─────────────────────────┐
│  1. Issue Backing       │      │  2. Development & PR    │      │  3. Automatic Reward    │
│  Sponsors pledge        ├─────►│  Developer resolves     ├─────►│  Polar automatically    │
│  bounty on GitHub Issue │      │  issue via PR (DCO)     │      │  disburses reward       │
└─────────────────────────┘      └─────────────────────────┘      └─────────────────────────┘
```

1. **Feature Bounties**: Enterprises or developers pledge funding onto a specific GitHub issue in `cordanaLLM/praetor`.
2. **Contributor Rewards (Split)**: When a contributor submits a PR that resolves the issue, maintainers allocate the reward split transparently to the author.
3. **Automated Tax & Payout Handling**: Polar.sh acts as Merchant of Record (MoR), managing global VAT/sales tax compliance and payouts via Stripe Connect.

---

## 🛠️ For Developers & Contributors: Earn Bounties

Want to contribute to Praetor and get compensated for resolved issues?

1. Browse the active bounties on the [bounty board](#bounty-board).
2. Pick an issue with active funding.
3. Submit your PR following our [Contribution Guidelines](guides/contributing.md) (DCO `Signed-off-by:` signature & HISS Quality Gates).
4. Upon PR merge, the bounty reward is credited directly to your Polar.sh account balance.

---

## 🏢 For Enterprises: Prioritize Features

Does your organization require a specific `standardsctl` context target or `standards-mcp` transport adapter?

1. Open an issue or select an existing roadmap item.
2. Click the **Polar.sh Fund Button** in the issue or open the [bounty board](#bounty-board).
3. Pledge your funding target.
4. Receive automated delivery notifications and formal enterprise invoices.

---

## Bounty board

<!-- praetor:funding-bounties:start -->
> No Polar.sh account is configured, so this site links no bounty board. The operator names one as `polar` in `.config/operator/funding.yaml`.
<!-- praetor:funding-bounties:end -->
