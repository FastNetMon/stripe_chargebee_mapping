# Introduction

Tool to enrich Stripe's payouts with customer information and VAT codes from Chargebee

# Build on Linux

```
CGO_ENABLED=0 go build -o stripe_chargebee_mapping_linux
```

# Build on Linux for MacOS Intel CPU based

```
GOOS=darwin GOARCH=amd64 go build -o stripe_chargebee_mapping_macos_amd64
```

# Build on Linux for MacOS Apple CPU based

```
GOOS=darwin GOARCH=arm64 go build -o stripe_chargebee_mapping_macos_arm64
```

# Stripe API key generation.

To use this tool you need to open Stripe interface and generate API access key. To do so open “Developers” on left panel then API key, then “Create restricted key” and after that enable Read permission for following capabilities:

- Balance
- Customers
- Payouts
- Transfers

Then fill in the key name "stripe_chargebee_key" and then click create key. Then save the generated key to file .stripe_key in the home folder. 

# Chargebee key generation

After logging into Chargebee select "Live site / test site", select Live. Settings, Configure Chargebee, API Keys, Add API key, Read Only Key, set flag Restricted, "Allow read only access to transactional data" and select name "chargebee_stripe". After that save the key to file .chargeebee_key in the user home folder. 

# Run

```
bin/stripe_chargebee_mapping -period_start 1569888000 -period_end 1577836799
```

# Example outout:

```
Date Arrived: 04/12/2021 Amount: 163.28 gbp 
Net amount: 82.66 gbp Company: XXX Country: United States
Net amount: 80.62 gbp Company: XXX Country: United Kingdom VAT: XXXXX Validation status: valid
```


