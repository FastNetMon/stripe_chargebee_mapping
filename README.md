# Introduction

Tool to enrich Stripe's payouts with customer information and VAT codes from Chargebee

# Build on Linux

```
go build
```

# Build on Linux for MacOS / cross compilation

```
GOOS=darwin GOARCH=amd64 go build
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


