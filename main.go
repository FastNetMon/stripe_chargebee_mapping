package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
    "os"
    "os/user"
	"regexp"
	"strings"
	"time"

	"github.com/chargebee/chargebee-go"
    "github.com/chargebee/chargebee-go/enum"
	transactionEnum "github.com/chargebee/chargebee-go/models/transaction/enum"
    customerAction "github.com/chargebee/chargebee-go/actions/customer"
	customer "github.com/chargebee/chargebee-go/models/customer"
    transactionModel "github.com/chargebee/chargebee-go/models/transaction"
    transactionAction "github.com/chargebee/chargebee-go/actions/transaction"
    "github.com/chargebee/chargebee-go/filter"
    "github.com/pariz/gountries"
	"github.com/pkg/errors"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/balancetransaction"
	"github.com/stripe/stripe-go/payout"
	"github.com/vjeantet/jodaTime"
)

var start = flag.Int("period_start", 0, "start of report period")
var end = flag.Int("period_end", 0, "end of report period")
var report_type = flag.String("query_type", "stripe_vat", "Type of query: stripe_vat or paypal_vat")

func main() {
	flag.Parse()

	if *start == 0 {
		log.Fatal("Please specify period start date")
	}

	if *end == 0 {
		log.Fatal("Please specify period end date")
	}

	usr, err := user.Current()

	if err != nil {
		log.Fatal("Cannot get current user")
	}

	stripe_key_path := usr.HomeDir + "/.stripe_key"

	data, err := ioutil.ReadFile(stripe_key_path)

	if err != nil {
		log.Fatalf("Cannot read Stripe key from file %s with error %v", stripe_key_path, err)
	}

	stripe.Key = strings.TrimSpace(string(data))

	chargebee_key_path := usr.HomeDir + "/.chargebee_key"

	dataChargebee, err := ioutil.ReadFile(chargebee_key_path)

	if err != nil {
		log.Fatalf("Cannot read Chargebee key from file %s with error %v", chargebee_key_path, err)
	}

	chargebee.Configure(strings.TrimSpace(string(dataChargebee)), "fastnetmon")

	// Supported log levels: LevelDebug, LevelInfo, LevelWarn, LevelError
	stripe.DefaultLeveledLogger = &stripe.LeveledLogger{
		Level: stripe.LevelError,
	}

    if *report_type == "paypal_vat" {
        params := &transactionModel.ListRequestParams{
		    Date: &filter.TimestampFilter{
			    Between: []int64{int64(*start), int64(*end)},
		    },
	        
            Gateway: &filter.EnumFilter{
                Is: enum.GatewayPaypalExpressCheckout,
            },
            Status: &filter.EnumFilter{
                Is: transactionEnum.StatusSuccess, // We need only successful ones
            },
            SortBy: &filter.SortFilter{
			    Asc:  string("date"),
		    },
            Limit: chargebee.Int32(100), // Max 100 per call, you may need to paginate
	    } 

        result, err := transactionAction.List(params).ListRequest()

	    if err != nil {
		    log.Fatalf("Error retrieving transactions: %v\n", err)
		    os.Exit(0)
	    }

        for _, entry := range result.List {
	    	txn := entry.Transaction
		    txnDate := time.Unix(txn.Date, 0).UTC() // Convert back to time.Time for readability

            customer, err := GetChargebeeUser(txn.CustomerId)

            if err != nil {
                log.Fatalf("Cannot get customer information for ID %s: %v", txn.CustomerId, err)
            }

            if customer.BillingAddress == nil {
                log.Printf("Unexpected empty billing address for %s", txn.CustomerId)
                continue
            }

            fmt.Printf("Date %s Amount: %v %s, Company: %s Country: %s VAT: %v\n",
                txnDate.Format(time.RFC3339),
			    txn.Amount/100,
                txn.CurrencyCode,
                customer.BillingAddress.Company,
                customer.BillingAddress.Country,
                fmt.Sprintf("VAT: %s Validation status: %s", customer.VatNumber, customer.VatNumberStatus),
		    )
	    }
	
        // TODO: Pagination when we have more then 100 results is not supported yet
	    if result.NextOffset != "" {
		    log.Fatalf("\nNote: There are more results. Use the NextOffset (%s) for the next API call.\n", result.NextOffset)
	    }

        os.Exit(0)
    } else if *report_type == "stripe_vat"  {
        // Keep going down
    } else {
        log.Fatalf("Unknown report type: %s", *report_type)
    }

	i := payout.List(&stripe.PayoutListParams{ArrivalDateRange: &stripe.RangeQueryParams{
		GreaterThanOrEqual: int64(*start),
		LesserThanOrEqual:  int64(*end),
	}})

	payouts := []*stripe.Payout{}

	for i.Next() {
		payout := i.Payout()
		payouts = append(payouts, payout)
	}

	if err := i.Err(); err != nil {
		log.Fatalf("Cannot retrieve all payouts: %v", err)
	}

	payoutsReverse := []*stripe.Payout{}

	for i := len(payouts) - 1; i >= 0; i-- {
		payoutsReverse = append(payoutsReverse, payouts[i])
	}

	for _, payout := range payoutsReverse {
		timeArrived := time.Unix(payout.ArrivalDate, 0)

		dateArrived := jodaTime.Format("dd/MM/YYYY", timeArrived)

		payment_result := ""

		if payout.FailureCode != "" {
			payment_result = fmt.Sprintf("Failed due to reason: %s", payout.FailureCode)
		}

		fmt.Printf("Date Arrived: %v Amount: %s %s %s\n", dateArrived, PrintAmount(payout.Amount), payout.Currency, payment_result)

		// Get sub transactions for this payout
		transactions, err := GetPayoutTransactions(payout.ID)

		if err != nil {
			log.Fatalf("Cannot get transactions: %v", err)
		}

		for _, txn := range transactions {
			// "ChargeBee customer: JXXXX111YYY777ZZZ"
			re := regexp.MustCompile(`ChargeBee customer: \w+`)
			chargebeeUser := re.FindString(txn.Description)

			if chargebeeUser == "" {
				if txn.Description == "REFUND FOR PAYOUT (STRIPE PAYOUT)" {
					fmt.Printf("Net amount: %v It's probably refund transaction retry from previously failed refunds\n", PrintAmount(txn.Net))
					continue
				} else if txn.Type == "refund_failure" {
					fmt.Printf("Net amount: %v It's probably refund transaction\n", PrintAmount(txn.Net))
					continue
				} else if txn.Type == "stripe_fee" {
					fmt.Printf("Net amount: %v Radar for Fraud teams fee, it's Stripe fee for fraud screening\n", PrintAmount(txn.Net))
					continue
				} else if txn.Type == "adjustment" && txn.ReportingCategory == "dispute" {
					// &{Amount:-206192 AvailableOn:1717696135 Created:1717696135 Currency:gbp
					// Description:Chargeback withdrawal for ch_xxx ExchangeRate:0 ID:txn_xxx
					// Fee:2000 FeeDetails:[0x14000d46050] Net:-208192 Recipient: ReportingCategory:dispute
					// Source:0x14000531570 Status:available Type:adjustment}
					fmt.Printf("Net amount: %v It's probably chargeback transaction\n", PrintAmount(txn.Net))
					continue
                } else if txn.Type == "adjustment" && txn.Description == "Contribution from reserved balance" && txn.Amount == 0 && txn.Fee == 0 {
                    // I suppose it's some kind of Stripe internal transaction which has no meaning for accounting purposes
                    continue
				} else {
					log.Fatalf("Unexpected issue with match from description: '%s' for transaction: %+v", txn.Description, txn)
				}
			}

			// Cut prefix
			chargebeeUser = strings.TrimPrefix(chargebeeUser, "ChargeBee customer: ")

			user, err := GetChargebeeUser(chargebeeUser)

			if err != nil {
				log.Fatalf("Cannot get user id from transaction description string: %s", txn.Description)
			}

			vatSection := ""

			if user.VatNumber != "" {
				vatSection = fmt.Sprintf("VAT: %s Validation status: %s", user.VatNumber, user.VatNumberStatus)
			}

			query := gountries.New()

			full_country_name := ""

			// Due to following issue: https://github.com/pariz/gountries/issues/30
			// We have to handle Kosovo in a special way

			if user.BillingAddress.Country == "XK" {
				full_country_name = "Kosovo"
			} else {
				country_lookup_res, err := query.FindCountryByAlpha(user.BillingAddress.Country)

				if err != nil {
					log.Fatalf("Cannot get country name for code: %s %v", user.BillingAddress.Country, err)
				}

				full_country_name = country_lookup_res.Name.BaseLang.Common
			}

			fmt.Printf("Net amount: %v %s Company: %s Country: %s %s\n", PrintAmount(txn.Net), txn.Currency, user.BillingAddress.Company, full_country_name, vatSection)
		}

		fmt.Printf("\n")
	}
}

// Returns absolute value for negative or positive values
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func PrintAmount(amount int64) string {
	// Amount can be negative
	return fmt.Sprintf("%d.%02d", amount/100, abs(amount)%100)
}

// Returns all sub-transactions from payout
func GetPayoutTransactions(payoutID string) ([]*stripe.BalanceTransaction, error) {

	j := balancetransaction.List(&stripe.BalanceTransactionListParams{Payout: &payoutID})

	payoutTransactions := []*stripe.BalanceTransaction{}

	for j.Next() {
		txn := j.BalanceTransaction()

		// List of transactions includes transaction itself
		// We should filetr it out, we need only nested transactions, they have type "charge"
		if txn.Type == "payout" {
			continue
		}

		payoutTransactions = append(payoutTransactions, txn)
	}

	if err := j.Err(); err != nil {
		return nil, errors.Errorf("Cannot get sub transactions: %v", err)
	}

	return payoutTransactions, nil
}

// Retrives user from Chargebee
func GetChargebeeUser(userID string) (*customer.Customer, error) {

	customerRetrieved, err := customerAction.Retrieve(userID).Request()

	if err != nil {
		return nil, err
	}

	return customerRetrieved.Customer, nil
}
