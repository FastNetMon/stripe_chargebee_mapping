package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go"

	"github.com/sendgrid/sendgrid-go/helpers/mail"

	subscription "github.com/chargebee/chargebee-go/models/subscription"

	subscriptionAction "github.com/chargebee/chargebee-go/actions/subscription"

	"github.com/chargebee/chargebee-go"
	customerAction "github.com/chargebee/chargebee-go/actions/customer"
	transactionAction "github.com/chargebee/chargebee-go/actions/transaction"
	"github.com/chargebee/chargebee-go/enum"
	"github.com/chargebee/chargebee-go/filter"
	customer "github.com/chargebee/chargebee-go/models/customer"
	transactionModel "github.com/chargebee/chargebee-go/models/transaction"
	transactionEnum "github.com/chargebee/chargebee-go/models/transaction/enum"
	"github.com/pariz/gountries"
	"github.com/pkg/errors"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/balancetransaction"
	"github.com/stripe/stripe-go/payout"
	"github.com/vjeantet/jodaTime"
)

type Config struct {
	SendGridEmailKey string `json:"sendgrid_email_key"`
}

var config Config

var start = flag.Int("period_start", 0, "start of report period")
var end = flag.Int("period_end", 0, "end of report period")
var period = flag.String("period", "", "Predefined period: last_month, last_quarter")
var report_type = flag.String("query_type", "stripe_vat", "Type of query: stripe_vat, paypal_vat, referral_transactions")

func send_email_no_tracking_no_hubspot(email_from string, email_target string, subject string, text string, html string, attachment_file string) error {
	from := mail.NewEmail("FastNetMon Team", email_from)
	to := mail.NewEmail("FastNetMon User", email_target)

	message := mail.NewSingleEmail(from, subject, to, text, html)

	falseFlag := false

	// Disable all kinds of tracking for these emails
	message.TrackingSettings = &mail.TrackingSettings{
		ClickTracking: &mail.ClickTrackingSetting{Enable: &falseFlag},
		OpenTracking:  &mail.OpenTrackingSetting{Enable: &falseFlag},
	}

	if attachment_file != "" {
		fileData, err := os.ReadFile(attachment_file)
		if err != nil {
			return fmt.Errorf("cannot read attachment file %s: %w", attachment_file, err)
		}

		attachment := mail.NewAttachment()
		attachment.SetContent(base64.StdEncoding.EncodeToString(fileData))
		attachment.SetType("text/plain")
		attachment.SetFilename(attachment_file[strings.LastIndex(attachment_file, "/")+1:])
		attachment.SetDisposition("attachment")

		message.AddAttachment(attachment)
	}

	client := sendgrid.NewSendClient(config.SendGridEmailKey)

	response, err := client.Send(message)

	if err != nil {
		return err
	}

	_ = response

	return nil
}

func calculatePayPalVAT() {
	transactions, err := get_all_paypal_transactions_period("", int64(*start), int64(*end))

	if err != nil {
		log.Fatalf("Cannot get all PayPal transactions: %v", err)
	}

	for _, txn := range transactions {
		txnDate := time.Unix(txn.Date, 0).UTC() // Convert back to time.Time for readability

		customer, err := GetChargebeeUser(txn.CustomerId)

		if err != nil {
			log.Fatalf("Cannot get customer information for ID %s: %v", txn.CustomerId, err)
		}

		if customer.BillingAddress == nil {
			log.Printf("Unexpected empty billing address for %s", txn.CustomerId)
			continue
		}

		vat_info := ""

		// Only for UK based customer we show VAT rate
		if customer.BillingAddress.Country == "GB" {
			vat_info = fmt.Sprintf("VAT: %s Rate: 20%%", customer.VatNumber)
		}

		fmt.Printf("Date %s Amount: %.2f %s, Company: %s Country: %s %v\n",
			txnDate.Format(time.RFC3339),
			float64(txn.Amount)/100,
			txn.CurrencyCode,
			customer.BillingAddress.Company,
			customer.BillingAddress.Country,
			vat_info,
		)
	}
}

func getReferralTransactions() {
	transactions, err := get_all_transactions_period("", int64(*start), int64(*end))

	if err != nil {
		log.Fatalf("Cannot get all transactions: %v", err)
	}

	total_value := 0.0

	for _, txn := range transactions {
		txnDate := time.Unix(txn.Date, 0).UTC() // Convert back to time.Time for readability

		customer, err := GetChargebeeUser(txn.CustomerId)

		if err != nil {
			log.Fatalf("Cannot get customer information for ID %s: %v", txn.CustomerId, err)
		}

		subscription, err := GetChargebeeSubscription(txn.SubscriptionId)

		if err != nil {
			log.Fatalf("Cannot get subscription information for ID %s: %v", txn.SubscriptionId, err)
		}

		// No referral information
		if len(subscription.CustomField) == 0 {
			continue
		}

		// We do have referral information
		referrer := subscription.CustomField["cf_referrer_name"]

		// We do not have one
		if referrer == nil {
			continue
		}

		fmt.Printf("%s Amount: %.2f %s Company: %s Subscription ID: %s Referrer: %v\n",
			txnDate.Format(time.RFC3339),
			float64(txn.Amount)/100,
			txn.CurrencyCode,
			customer.BillingAddress.Company,
			txn.SubscriptionId,
			referrer,
		)

		total_value += float64(txn.Amount) / 100
	}

	log.Printf("Total value: %2.f", total_value)
}

func calculateStripeVAT() {

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
				} else if txn.Type == "adjustment" && txn.Description == "Hold in reserved balance" && txn.Amount == 0 && txn.Fee == 0 {
					// I suppose it's some kind of Stripe internal transaction which has no meaning for accounting purposes
					continue
				} else if txn.Type == "payout_minimum_balance_hold" && txn.ReportingCategory == "payout_minimum_balance_hold" {
					fmt.Printf("Stripe hold part of payment to maintain balance: %v\n", PrintAmount(txn.Net))
					continue
				} else if txn.Type == "payout_minimum_balance_release" && txn.ReportingCategory == "payout_minimum_balance_release" {
					fmt.Printf("Stripe released part of payment to maintain balance: %v\n", PrintAmount(txn.Net))
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

func main() {
	flag.Parse()

	configData, err := os.ReadFile("/etc/stripe_chargebee_mapping.conf")
	if err != nil {
		log.Fatalf("Cannot read config file /etc/stripe_chargebee_mapping.conf: %v", err)
	}

	if err := json.Unmarshal(configData, &config); err != nil {
		log.Fatalf("Cannot parse config file /etc/stripe_chargebee_mapping.conf: %v", err)
	}

	if *period == "last_month" {
		now := time.Now().UTC()
		firstOfCurrentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		firstOfLastMonth := firstOfCurrentMonth.AddDate(0, -1, 0)

		*start = int(firstOfLastMonth.Unix())
		*end = int(firstOfCurrentMonth.Add(-time.Second).Unix())
	} else if *period == "last_quarter" {
		now := time.Now().UTC()
		currentQuarter := (int(now.Month()) - 1) / 3 // 0-based quarter index
		firstMonthOfCurrentQuarter := time.Month(currentQuarter*3 + 1)
		firstOfCurrentQuarter := time.Date(now.Year(), firstMonthOfCurrentQuarter, 1, 0, 0, 0, 0, time.UTC)
		firstOfLastQuarter := firstOfCurrentQuarter.AddDate(0, -3, 0)

		*start = int(firstOfLastQuarter.Unix())
		*end = int(firstOfCurrentQuarter.Add(-time.Second).Unix())
	} else if *period != "" {
		log.Fatalf("Unknown period: %s. Supported: last_month, last_quarter", *period)
	}

	if *start == 0 {
		log.Fatal("Please specify period start date or use -period last_month or -period last_quarter")
	}

	if *end == 0 {
		log.Fatal("Please specify period end date or use -period last_month or -period last_quarter")
	}

	usr, err := user.Current()

	if err != nil {
		log.Fatal("Cannot get current user")
	}

	stripe_key_path := usr.HomeDir + "/.stripe_key"

	data, err := os.ReadFile(stripe_key_path)

	if err != nil {
		log.Fatalf("Cannot read Stripe key from file %s with error %v", stripe_key_path, err)
	}

	stripe.Key = strings.TrimSpace(string(data))

	chargebee_key_path := usr.HomeDir + "/.chargebee_key"

	dataChargebee, err := os.ReadFile(chargebee_key_path)

	if err != nil {
		log.Fatalf("Cannot read Chargebee key from file %s with error %v", chargebee_key_path, err)
	}

	chargebee.Configure(strings.TrimSpace(string(dataChargebee)), "fastnetmon")

	// Supported log levels: LevelDebug, LevelInfo, LevelWarn, LevelError
	stripe.DefaultLeveledLogger = &stripe.LeveledLogger{
		Level: stripe.LevelError,
	}

	if *report_type == "paypal_vat" {
		calculatePayPalVAT()

		os.Exit(0)
	} else if *report_type == "referral_transactions" {
		getReferralTransactions()

		os.Exit(0)
	} else if *report_type == "stripe_vat" {
		calculateStripeVAT()

		os.Exit(0)
	} else {
		log.Fatalf("Unknown report type: %s", *report_type)
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

func get_all_paypal_transactions_period(offset string, start_period int64, end_period int64) ([]*transactionModel.Transaction, error) {
	params := &transactionModel.ListRequestParams{
		Date: &filter.TimestampFilter{
			Between: []int64{int64(start_period), int64(end_period)},
		},

		Gateway: &filter.EnumFilter{
			Is: enum.GatewayPaypalExpressCheckout,
		},
		Status: &filter.EnumFilter{
			Is: transactionEnum.StatusSuccess, // We need only successful ones
		},
		SortBy: &filter.SortFilter{
			Asc: string("date"),
		},
		Limit:  chargebee.Int32(100), // Max 100 per call, you may need to paginate
		Offset: offset,
	}

	result, err := transactionAction.List(params).ListRequest()

	if err != nil {
		return nil, fmt.Errorf("Error retrieving transactions: %v\n", err)
	}

	transactions := []*transactionModel.Transaction{}

	for _, entry := range result.List {
		txn := entry.Transaction

		transactions = append(transactions, txn)
	}

	if result.NextOffset != "" {
		another_page, err := get_all_paypal_transactions_period(result.NextOffset, start_period, end_period)

		if err != nil {
			return nil, fmt.Errorf("Cannot get second page: %v", err)
		}

		transactions = append(transactions, another_page...)
	}

	return transactions, nil
}

func get_all_transactions_period(offset string, start_period int64, end_period int64) ([]*transactionModel.Transaction, error) {
	params := &transactionModel.ListRequestParams{
		Date: &filter.TimestampFilter{
			Between: []int64{int64(start_period), int64(end_period)},
		},
		Status: &filter.EnumFilter{
			Is: transactionEnum.StatusSuccess, // We need only successful ones
		},
		SortBy: &filter.SortFilter{
			Asc: string("date"),
		},
		Limit:  chargebee.Int32(100), // Max 100 per call, you may need to paginate
		Offset: offset,
	}

	result, err := transactionAction.List(params).ListRequest()

	if err != nil {
		return nil, fmt.Errorf("Error retrieving transactions: %v\n", err)
	}

	transactions := []*transactionModel.Transaction{}

	for _, entry := range result.List {
		txn := entry.Transaction

		transactions = append(transactions, txn)
	}

	if result.NextOffset != "" {
		another_page, err := get_all_transactions_period(result.NextOffset, start_period, end_period)

		if err != nil {
			return nil, fmt.Errorf("Cannot get second page: %v", err)
		}

		transactions = append(transactions, another_page...)
	}

	return transactions, nil
}

// Retrieves subscription from Chargebee
func GetChargebeeSubscription(subscriptionID string) (*subscription.Subscription, error) {

	subscriptionRetrieved, err := subscriptionAction.Retrieve(subscriptionID).Request()

	if err != nil {
		return nil, err
	}

	return subscriptionRetrieved.Subscription, nil
}
