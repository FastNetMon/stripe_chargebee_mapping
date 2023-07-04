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


