package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/chargebee/chargebee-go"
	customerAction "github.com/chargebee/chargebee-go/actions/customer"
	customer "github.com/chargebee/chargebee-go/models/customer"
	"github.com/pariz/gountries"
	"github.com/pkg/errors"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/balancetransaction"
	"github.com/stripe/stripe-go/payout"
	"github.com/vjeantet/jodaTime"
)

var start = flag.Int("period_start", 0, "start of report period")
var end = flag.Int("period_end", 0, "end of report period")

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
					fmt.Printf("Net amount: %v Radar for Fraud teams fee, it's Stripe fee for fraud scereening\n", PrintAmount(txn.Net))
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

			full_country_name, err := query.FindCountryByAlpha(user.BillingAddress.Country)

			if err != nil {
				log.Fatalf("Cannot get country name for code: %s %v", user.BillingAddress.Country, err)
			}

			fmt.Printf("Net amount: %v %s Company: %s Country: %s %s\n", PrintAmount(txn.Net), txn.Currency, user.BillingAddress.Company, full_country_name.Name.BaseLang.Common, vatSection)
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
